package importer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"goonhub-stash-importer/internal/goonhub"
	"goonhub-stash-importer/internal/stash"
	"goonhub-stash-importer/internal/state"
)

var errSkip = errors.New("skipped by configuration")

func (imp *Importer) sceneQuery() stash.SceneQuery {
	return stash.SceneQuery{Filter: imp.cfg.Scenes.Filter, IDs: imp.cfg.Scenes.IDs, Sort: imp.cfg.Scenes.Sort, PerPage: imp.cfg.Stash.PageSize}
}

// eachScene streams Stash scenes page by page to fn, using cfg.Concurrency workers.
func (imp *Importer) eachScene(ctx context.Context, label string, fn func(context.Context, stash.Scene)) error {
	sq := imp.sceneQuery()
	first, total, err := imp.stash.ScenesPage(ctx, sq, 1)
	if err != nil {
		return err
	}
	if lim := imp.cfg.Scenes.Limit; lim > 0 && total > lim {
		total = lim
	}
	imp.scenesTotal.Store(int64(total))
	imp.log.Info(label, "scenes", total, "workers", imp.cfg.Concurrency, "page_size", sq.PerPage)

	jobs := make(chan stash.Scene, sq.PerPage)
	var wg sync.WaitGroup
	for i := 0; i < imp.cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sc := range jobs {
				if ctx.Err() != nil {
					continue // drain
				}
				fn(ctx, sc)
			}
		}()
	}

	var fetchErr error
	sent := 0
	page, scenes := 1, first
produce:
	for {
		for _, sc := range scenes {
			if sent >= total {
				break produce
			}
			select {
			case jobs <- sc:
				sent++
			case <-ctx.Done():
				break produce
			}
		}
		if sent >= total || len(scenes) == 0 {
			break
		}
		page++
		if scenes, _, fetchErr = imp.stash.ScenesPage(ctx, sq, page); fetchErr != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if fetchErr != nil {
		return fetchErr
	}
	return ctx.Err()
}

func (imp *Importer) importScenes(ctx context.Context) error {
	prog := newProgress(imp.log, "scenes", imp.scenesTotal.Load, imp.cfg.Log.ProgressEvery)
	return imp.eachScene(ctx, "importing scenes", func(ctx context.Context, sc stash.Scene) {
		imp.processScene(ctx, sc)
		prog.tick(imp.rep.S("scenes"))
	})
}

// planScene builds the GoonHub request from a Stash scene.
func (imp *Importer) planScene(sc stash.Scene) (*goonhub.ImportSceneRequest, error) {
	if len(sc.Files) == 0 {
		return nil, errors.New("scene has no file")
	}
	f := sc.Files[0] // Stash returns the primary file first
	imp.rep.Drop("scene.extra_files (only the primary file is imported)", len(sc.Files)-1)

	mapped, err := imp.pm.Map(f.Path)
	if err != nil {
		if imp.cfg.Scenes.UnmappedPath == "skip" {
			return nil, errSkip
		}
		return nil, err
	}
	title := titleFor(sc.Title, f.Basename, imp.cfg.Scenes.TitleFallback)
	release, ok := normalizeDate(sc.Date)
	if !ok {
		imp.rep.Warn("scenes", sc.ID, title, "date", "invalid date %q ignored", deref(sc.Date))
	}
	r := &goonhub.ImportSceneRequest{
		Title:            title,
		StoredPath:       mapped.Path,
		OriginalFilename: f.Basename,
		Size:             f.Size,
		Duration:         int(math.Round(f.Duration)),
		Width:            f.Width,
		Height:           f.Height,
		FrameRate:        f.FrameRate,
		BitRate:          f.BitRate,
		VideoCodec:       f.VideoCodec,
		AudioCodec:       f.AudioCodec,
		Description:      deref(sc.Details),
		ReleaseDate:      release,
		Origin:           imp.cfg.Scenes.Origin,
		Type:             imp.cfg.Scenes.Type,
		SkipFileCheck:    imp.cfg.Scenes.SkipFileCheck,
		FileCreatedAt:    rfc3339(f.ModTime),
		TagIDs:           []uint{},
		ActorIDs:         []uint{},
	}
	if mapped.StoragePathID > 0 {
		r.StoragePathID = ptr(mapped.StoragePathID)
	}
	if imp.cfg.Scenes.PreserveCreatedAt {
		r.CreatedAt = rfc3339(&sc.CreatedAt)
	}
	if imp.v2 && imp.cfg.PostImport.Reindex {
		r.SkipIndex = true // one bulk reindex at the end is much faster
	}
	if deref(sc.Code) != "" {
		imp.rep.Drop("scene.code", 1)
	}
	if deref(sc.Director) != "" {
		imp.rep.Drop("scene.director", 1)
	}
	imp.rep.Drop("scene.urls", len(sc.URLs))
	if sc.Organized {
		imp.rep.Drop("scene.organized flag", 1)
	}

	if sc.Studio != nil {
		if id, ok := imp.st.GHID(state.KindStudios, sc.Studio.ID); ok {
			r.StudioID = ptr(id)
		} else {
			imp.rep.Warn("scenes", sc.ID, title, "studio", "studio stash:%s is not imported", sc.Studio.ID)
		}
	}
	seen := map[uint]bool{}
	var tagNames []string
	for _, t := range sc.Tags {
		if imp.excludedTag[t.ID] {
			continue
		}
		tagNames = append(tagNames, imp.tagName[t.ID])
		id, ok := imp.st.GHID(state.KindTags, t.ID)
		if !ok {
			imp.rep.Warn("scenes", sc.ID, title, "tags", "tag stash:%s (%s) is not imported", t.ID, imp.tagName[t.ID])
			continue
		}
		if !seen[id] {
			seen[id] = true
			r.TagIDs = append(r.TagIDs, id)
		}
	}
	for _, p := range sc.Performers {
		id, ok := imp.st.GHID(state.KindPerformers, p.ID)
		if !ok {
			imp.rep.Warn("scenes", sc.ID, title, "actors", "performer stash:%s is not imported", p.ID)
			continue
		}
		r.ActorIDs = append(r.ActorIDs, id)
	}
	sort.Slice(r.TagIDs, func(i, j int) bool { return r.TagIDs[i] < r.TagIDs[j] })
	sort.Slice(r.ActorIDs, func(i, j int) bool { return r.ActorIDs[i] < r.ActorIDs[j] })
	for _, rule := range imp.cfg.Scenes.TypeRules {
		if containsFold(tagNames, rule.Tag) {
			r.Type = rule.Type
			break
		}
	}
	if imp.cfg.Phases.Interactions {
		r.Interactions = imp.sceneInteractions(sc)
		if sc.PlayCount != nil && *sc.PlayCount > 0 && imp.cfg.Interactions.PlayCount {
			r.ViewCount = ptr(int64(*sc.PlayCount))
		}
		if !imp.v2 {
			if r.ViewCount != nil {
				imp.rep.Drop("scene.play_count (needs import API v2)", 1)
			}
			if r.Interactions != nil {
				imp.rep.Drop("scene.play_history entries (needs import API v2)", len(r.Interactions.Watches))
			}
		}
	}
	return r, nil
}

func (imp *Importer) sceneInteractions(sc stash.Scene) *goonhub.Interactions {
	ic := imp.cfg.Interactions
	in := &goonhub.Interactions{}
	if ic.Rating {
		in.Rating = ratingStars(sc.Rating100)
	}
	if ic.OCounter && sc.OCounter != nil && *sc.OCounter > 0 {
		in.JizzedCount = ptr(*sc.OCounter)
		if imp.jizzHistory {
			// Dated "o" events, sorted and unique (the server dedups by instant too)
			seen := map[string]bool{}
			for _, ts := range sc.OHistory {
				at := rfc3339(&ts)
				if at == nil {
					imp.rep.Drop("scene.o_history unparsable timestamps", 1)
					continue
				}
				if !seen[*at] {
					seen[*at] = true
					in.JizzHistory = append(in.JizzHistory, *at)
				}
			}
			sort.Strings(in.JizzHistory)
		} else {
			imp.rep.Drop("scene.o_history timestamps (count kept)", len(sc.OHistory))
		}
	}
	if ic.PlayHistory && len(sc.PlayHistory) > 0 {
		// GoonHub keeps one watch per second (idempotent replays); Stash may hold
		// several plays at the same second. The play count keeps the real total.
		bySecond := map[string]string{}
		for _, ts := range sc.PlayHistory {
			if at := rfc3339(&ts); at != nil {
				bySecond[*at] = ts
			}
		}
		imp.rep.Drop("scene.play_history same-second duplicates (count kept in view_count)", len(sc.PlayHistory)-len(bySecond))
		hist := make([]string, 0, len(bySecond))
		for _, ts := range bySecond {
			hist = append(hist, ts)
		}
		sort.Slice(hist, func(i, j int) bool { return *rfc3339(&hist[i]) < *rfc3339(&hist[j]) })
		per := 0
		if sc.PlayDuration != nil && *sc.PlayDuration > 0 {
			per = int(*sc.PlayDuration) / len(hist)
		}
		for i, ts := range hist {
			at := rfc3339(&ts)
			if at == nil {
				continue
			}
			w := goonhub.Watch{WatchedAt: *at, WatchDuration: per}
			if i == len(hist)-1 && ic.Resume && sc.ResumeTime != nil {
				w.LastPosition = int(*sc.ResumeTime)
			}
			in.Watches = append(in.Watches, w)
		}
	} else if ic.Resume && sc.ResumeTime != nil && *sc.ResumeTime > 0 {
		imp.rep.Drop("scene.resume_time without play history", 1)
	}
	if in.Rating == nil && in.JizzedCount == nil && len(in.Watches) == 0 {
		return nil
	}
	return in
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

func (imp *Importer) processScene(ctx context.Context, sc stash.Scene) {
	st := imp.rep.S("scenes")
	e, found := imp.st.Get(state.KindScenes, sc.ID)
	name := deref(sc.Title)
	if name == "" && len(sc.Files) > 0 {
		name = sc.Files[0].Basename
	}
	req, err := imp.planScene(sc)
	if errors.Is(err, errSkip) {
		st.Skipped.Add(1)
		return
	}
	if err != nil {
		imp.st.Put(state.KindScenes, sc.ID, state.Entry{GH: e.GH, Status: state.StatusFailed, Err: err.Error()})
		imp.rep.Error("scenes", sc.ID, name, "plan", err)
		st.Failed.Add(1)
		return
	}
	hash := hashOf(req)

	if !imp.cfg.Phases.Scenes || !imp.needsWork(e, found, hash) {
		if imp.cfg.Phases.Scenes {
			st.Unchanged.Add(1)
		}
		if e.GH != 0 && imp.cfg.Phases.Markers {
			imp.importMarkers(ctx, sc, e.GH)
		}
		return
	}

	if imp.cfg.DryRun {
		if req.Interactions != nil || req.ViewCount != nil {
			imp.rep.S("interactions").Updated.Add(1)
		}
		id := e.GH
		if id == 0 {
			id = imp.fakeID()
			st.Created.Add(1)
		} else {
			st.Updated.Add(1)
		}
		imp.st.Put(state.KindScenes, sc.ID, state.Entry{GH: id, Status: state.StatusOK, Hash: hash})
		if imp.cfg.Phases.Markers {
			imp.importMarkers(ctx, sc, id)
		}
		return
	}

	var id uint
	var created bool
	var partial []string
	if imp.v2 {
		id, created, err = imp.upsertSceneV2(ctx, req, found && e.GH != 0)
		if err == nil && (req.Interactions != nil || req.ViewCount != nil) {
			imp.rep.S("interactions").Updated.Add(1)
		}
	} else {
		id, created, partial, err = imp.upsertSceneV1(ctx, sc, req, found && e.GH != 0)
	}
	if err != nil {
		imp.st.Put(state.KindScenes, sc.ID, state.Entry{GH: e.GH, Status: state.StatusFailed, Err: err.Error()})
		imp.rep.Error("scenes", sc.ID, name, "import", err)
		st.Failed.Add(1)
		return
	}
	entry := state.Entry{GH: id, Status: state.StatusOK, Hash: hash, Img: e.Img}
	if created {
		imp.rep.SceneCreated(id)
		st.Created.Add(1)
	} else {
		st.Updated.Add(1)
	}
	if err := imp.sceneCover(ctx, sc, id, &entry); err != nil {
		partial = append(partial, "cover: "+err.Error())
	}
	if len(partial) > 0 {
		entry.Status, entry.Err, entry.Hash = state.StatusPartial, strings.Join(partial, "; "), ""
		st.Partial.Add(1)
	}
	imp.st.Put(state.KindScenes, sc.ID, entry)
	if imp.cfg.Phases.Markers {
		imp.importMarkers(ctx, sc, id)
	}
}

// upsertSceneV2 imports atomically; a scene already present at that path is
// adopted and updated (it may come from a lost state file or a GoonHub scan).
func (imp *Importer) upsertSceneV2(ctx context.Context, req *goonhub.ImportSceneRequest, known bool) (uint, bool, error) {
	r := *req
	r.UpdateExisting = known
	id, created, err := imp.gh.ImportScene(ctx, r)
	if _, conflict := goonhub.IsConflict(err); conflict {
		r.UpdateExisting = true
		id, _, err = imp.gh.ImportScene(ctx, r)
		created = false
	}
	return id, created, err
}

// upsertSceneV1 uses the v1 endpoint then separate association calls.
func (imp *Importer) upsertSceneV1(ctx context.Context, sc stash.Scene, req *goonhub.ImportSceneRequest, known bool) (uint, bool, []string, error) {
	r := *req
	r.TagIDs, r.ActorIDs, r.Interactions, r.ViewCount = nil, nil, nil, nil
	id, created, err := imp.gh.ImportScene(ctx, r)
	if ce, conflict := goonhub.IsConflict(err); conflict {
		if ce.ExistingID == 0 {
			return 0, false, nil, fmt.Errorf("scene exists in goonhub but its id was not returned: %w", err)
		}
		id, created, err = ce.ExistingID, false, nil
	}
	if err != nil {
		return 0, false, nil, err
	}
	var partial []string
	step := func(stage string, err error) {
		if err != nil {
			imp.rep.Error("scenes", sc.ID, req.Title, stage, err)
			partial = append(partial, stage+": "+err.Error())
		}
	}
	if !created {
		step("details", imp.gh.UpdateSceneDetails(ctx, id, req.Title, req.Description, req.ReleaseDate))
		step("studio", imp.gh.SetSceneStudio(ctx, id, req.StudioID))
	}
	if !created || len(req.TagIDs) > 0 {
		step("tags", imp.gh.SetSceneTags(ctx, id, req.TagIDs))
	}
	if !created || len(req.ActorIDs) > 0 {
		step("actors", imp.gh.SetSceneActors(ctx, id, req.ActorIDs))
	}
	if in := req.Interactions; in != nil {
		is := imp.rep.S("interactions")
		if in.Rating != nil || in.JizzedCount != nil {
			rating, _, jizzed, err := imp.gh.GetInteractions(ctx, id)
			step("interactions", err)
			if err == nil {
				if in.Rating != nil && rating != *in.Rating {
					err := imp.gh.SetRating(ctx, id, *in.Rating)
					step("rating", err)
					if err == nil {
						is.Updated.Add(1)
					}
				}
				for n := jizzed; in.JizzedCount != nil && n < *in.JizzedCount; n++ {
					if err := imp.gh.IncrementJizzed(ctx, id); err != nil {
						step("jizzed", err)
						break
					}
				}
			}
		}
	}
	return id, created, partial, nil
}

func (imp *Importer) sceneCover(ctx context.Context, sc stash.Scene, id uint, e *state.Entry) error {
	shot := deref(sc.Paths.Screenshot)
	if !imp.cfg.Images.SceneCovers || shot == "" || e.Img == shot {
		return nil
	}
	is := imp.rep.S("images")
	data, ctype, err := imp.stash.Download(ctx, shot)
	if err != nil {
		is.Failed.Add(1)
		imp.rep.Error("scenes", sc.ID, "", "cover_download", err)
		return err
	}
	if data == nil {
		return nil
	}
	if err := imp.gh.UploadThumbnail(ctx, id, "scene-"+sc.ID+extFor(ctype), data); err != nil {
		is.Failed.Add(1)
		imp.rep.Error("scenes", sc.ID, "", "cover_upload", err)
		return err
	}
	e.Img = shot
	is.Created.Add(1)
	return nil
}

// --- Markers ---

type markerPlan struct {
	Label  string `json:"l"`
	TS     int    `json:"t"`
	End    *int   `json:"e,omitempty"` // only set when the server stores marker ends
	Color  string `json:"c"`
	TagIDs []uint `json:"g"`
}

func (imp *Importer) planMarker(m stash.Marker) markerPlan {
	primary := ""
	if m.PrimaryTag != nil {
		primary = m.PrimaryTag.Name
	}
	p := markerPlan{Label: markerLabel(m.Title, primary), TS: int(math.Round(math.Max(m.Seconds, 0))), Color: imp.cfg.Markers.Color}
	if m.EndSeconds != nil && imp.markerEnd {
		// GoonHub stores whole seconds; a range that rounds to nothing stays a point marker.
		if end := int(math.Round(*m.EndSeconds)); end > p.TS {
			p.End = &end
		}
	}
	for k, v := range imp.cfg.Markers.ColorByTag {
		if strings.EqualFold(k, primary) {
			p.Color = v
		}
	}
	seen := map[uint]bool{}
	add := func(stashTagID string) {
		if imp.excludedTag[stashTagID] {
			return
		}
		if id, ok := imp.st.GHID(state.KindTags, stashTagID); ok && !seen[id] {
			seen[id] = true
			p.TagIDs = append(p.TagIDs, id)
		}
	}
	switch imp.cfg.Markers.Tags {
	case "primary_and_extra":
		if m.PrimaryTag != nil {
			add(m.PrimaryTag.ID)
		}
		fallthrough
	case "extra":
		for _, t := range m.Tags {
			add(t.ID)
		}
	}
	sort.Slice(p.TagIDs, func(i, j int) bool { return p.TagIDs[i] < p.TagIDs[j] })
	return p
}

func (imp *Importer) importMarkers(ctx context.Context, sc stash.Scene, sceneGH uint) {
	st := imp.rep.S("markers")
	var existing map[string]uint // v1 only, lazily loaded: "ts|label" -> id
	for _, m := range sc.SceneMarkers {
		if ctx.Err() != nil {
			return
		}
		p := imp.planMarker(m)
		hash := hashOf(p)
		e, found := imp.st.Get(state.KindMarkers, m.ID)
		if !imp.needsWork(e, found, hash) {
			st.Unchanged.Add(1)
			continue
		}
		if m.EndSeconds != nil && p.End == nil {
			imp.rep.Drop("marker.end_seconds", 1)
		}
		if imp.cfg.DryRun {
			if found && e.GH != 0 {
				st.Updated.Add(1)
			} else {
				st.Created.Add(1)
			}
			continue
		}
		req := goonhub.ImportMarkerRequest{SceneID: sceneGH, UserID: imp.userID, Timestamp: p.TS, EndTimestamp: p.End, Label: p.Label, Color: p.Color}
		var id uint
		var created bool
		var err error
		if imp.v2 {
			req.TagIDs, req.UpdateExisting = p.TagIDs, true // natural-key dedup server-side
			id, created, err = imp.gh.ImportMarker(ctx, req, true)
		} else {
			if existing == nil {
				existing = map[string]uint{}
				list, lerr := imp.gh.SceneMarkers(ctx, sceneGH)
				if lerr != nil {
					imp.rep.Error("markers", m.ID, p.Label, "list", lerr)
					st.Failed.Add(1)
					continue
				}
				for _, x := range list {
					existing[fmt.Sprintf("%d|%s", x.Timestamp, x.Label)] = x.ID
				}
			}
			key := fmt.Sprintf("%d|%s", p.TS, p.Label)
			if id = existing[key]; id == 0 {
				id, created, err = imp.gh.ImportMarker(ctx, req, false)
				if err == nil {
					existing[key] = id
				}
			}
			if err == nil && len(p.TagIDs) > 0 {
				if terr := imp.gh.AddMarkerTags(ctx, id, p.TagIDs); terr != nil {
					imp.rep.Error("markers", m.ID, p.Label, "tags", terr)
					imp.st.Put(state.KindMarkers, m.ID, state.Entry{GH: id, Status: state.StatusPartial, Err: terr.Error()})
					st.Partial.Add(1)
					continue
				}
			}
		}
		if ce, conflict := goonhub.IsConflict(err); conflict && ce.ExistingID != 0 {
			id, err = ce.ExistingID, nil
		}
		if err != nil {
			imp.st.Put(state.KindMarkers, m.ID, state.Entry{GH: e.GH, Status: state.StatusFailed, Err: err.Error()})
			imp.rep.Error("markers", m.ID, p.Label, "import", err)
			st.Failed.Add(1)
			continue
		}
		imp.st.Put(state.KindMarkers, m.ID, state.Entry{GH: id, Status: state.StatusOK, Hash: hash})
		switch {
		case created:
			st.Created.Add(1)
		case found:
			st.Updated.Add(1)
		default:
			st.Reused.Add(1)
		}
	}
}
