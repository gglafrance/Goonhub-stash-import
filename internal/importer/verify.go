package importer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"goonhub-stash-importer/internal/goonhub"
	"goonhub-stash-importer/internal/stash"
	"goonhub-stash-importer/internal/state"
)

type VerifyStats struct {
	Checked, OK, NotImported, Missing, Mismatch, Skipped atomic.Int64
}

// Verify re-reads GoonHub and compares every selected Stash scene (path, studio,
// tags, actors, markers and, in v2, interactions) with what was imported.
func (imp *Importer) Verify(ctx context.Context) (*VerifyStats, error) {
	vs := &VerifyStats{}
	tags, err := imp.stash.Tags(ctx, 1000)
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		imp.tagName[t.ID] = t.Name
		if imp.isExcludedTag(t.Name) {
			imp.excludedTag[t.ID] = true
		}
	}

	sq := imp.sceneQuery()
	limit := imp.cfg.Scenes.Limit
	seen := 0
	for page := 1; ; page++ {
		scenes, total, err := imp.stash.ScenesPage(ctx, sq, page)
		if err != nil {
			return vs, err
		}
		if limit > 0 && len(scenes) > limit-seen {
			scenes = scenes[:max(limit-seen, 0)]
		}
		if len(scenes) == 0 {
			break
		}
		seen += len(scenes)
		imp.verifyPage(ctx, scenes, vs)
		shown := total
		if limit > 0 && limit < total {
			shown = limit
		}
		imp.log.Info(fmt.Sprintf("[verify] %d/%d", seen, shown),
			"ok", vs.OK.Load(), "mismatch", vs.Mismatch.Load(), "not_imported", vs.NotImported.Load(), "missing_in_goonhub", vs.Missing.Load())
		if seen >= total || (limit > 0 && seen >= limit) || ctx.Err() != nil {
			break
		}
	}
	return vs, ctx.Err()
}

type expected struct {
	sc      stash.Scene
	req     *goonhub.ImportSceneRequest
	gh      uint
	markers int
}

func (imp *Importer) verifyPage(ctx context.Context, scenes []stash.Scene, vs *VerifyStats) {
	var exp []expected
	for _, sc := range scenes {
		req, err := imp.planScene(sc)
		if errors.Is(err, errSkip) {
			vs.Skipped.Add(1)
			continue
		}
		vs.Checked.Add(1)
		if err != nil {
			vs.NotImported.Add(1)
			imp.rep.Error("verify", sc.ID, "", "plan", err)
			continue
		}
		id, ok := imp.st.GHID(state.KindScenes, sc.ID)
		if !ok {
			vs.NotImported.Add(1)
			imp.rep.Add(Issue{Kind: "verify", StashID: sc.ID, Name: req.Title, Severity: "error", Stage: "not_imported", Message: "scene is not in the import state"})
			continue
		}
		keys := map[string]bool{}
		for _, m := range sc.SceneMarkers {
			p := imp.planMarker(m)
			keys[fmt.Sprintf("%d|%s", p.TS, p.Label)] = true
		}
		exp = append(exp, expected{sc: sc, req: req, gh: id, markers: len(keys)})
	}
	if len(exp) == 0 {
		return
	}

	summaries := map[uint]*goonhub.SceneSummary{}
	var mu sync.Mutex
	if imp.v2 {
		ids := make([]uint, len(exp))
		for i, e := range exp {
			ids[i] = e.gh
		}
		data, _, err := imp.gh.SceneSummaries(ctx, ids)
		if err != nil {
			imp.rep.Error("verify", "", "", "summary", err)
			return
		}
		for i := range data {
			summaries[data[i].ID] = &data[i]
		}
	} else {
		sem := make(chan struct{}, imp.cfg.Concurrency)
		var wg sync.WaitGroup
		for _, e := range exp {
			wg.Add(1)
			sem <- struct{}{}
			go func(id uint) {
				defer wg.Done()
				defer func() { <-sem }()
				s, err := imp.gh.GetScene(ctx, id)
				if err != nil {
					if !goonhub.IsNotFound(err) {
						imp.rep.Error("verify", "", "", "get_scene", err)
					}
					return
				}
				var errs []error
				var err1, err2, err3 error
				s.TagIDs, err1 = imp.gh.SceneTagIDs(ctx, id)
				s.ActorIDs, err2 = imp.gh.SceneActorIDs(ctx, id)
				ms, err3 := imp.gh.SceneMarkers(ctx, id)
				s.MarkerCount = len(ms)
				for _, e := range []error{err1, err2, err3} {
					if e != nil {
						errs = append(errs, e)
					}
				}
				if len(errs) > 0 {
					imp.rep.Error("verify", "", "", "get_scene", errors.Join(errs...))
					return
				}
				mu.Lock()
				summaries[id] = s
				mu.Unlock()
			}(e.gh)
		}
		wg.Wait()
	}

	for _, e := range exp {
		s := summaries[e.gh]
		if s == nil {
			vs.Missing.Add(1)
			imp.rep.Add(Issue{Kind: "verify", StashID: e.sc.ID, GHID: e.gh, Name: e.req.Title, Severity: "error", Stage: "missing", Message: "scene in state but not found in GoonHub"})
			continue
		}
		diffs := imp.compare(e, s)
		if len(diffs) == 0 {
			vs.OK.Add(1)
			continue
		}
		vs.Mismatch.Add(1)
		imp.rep.Add(Issue{Kind: "verify", StashID: e.sc.ID, GHID: e.gh, Name: e.req.Title, Severity: "error", Stage: "mismatch", Message: strings.Join(diffs, "; ")})
	}
}

func (imp *Importer) compare(e expected, s *goonhub.SceneSummary) []string {
	var d []string
	r := e.req
	if s.Trashed {
		d = append(d, "scene is in the trash")
	}
	if s.StoredPath != r.StoredPath {
		d = append(d, fmt.Sprintf("stored_path %q != %q", s.StoredPath, r.StoredPath))
	}
	if !eqUintPtr(s.StudioID, r.StudioID) {
		d = append(d, fmt.Sprintf("studio %v != %v", fmtPtr(s.StudioID), fmtPtr(r.StudioID)))
	}
	if !eqSet(s.TagIDs, r.TagIDs) {
		d = append(d, fmt.Sprintf("tags %v != %v", sorted(s.TagIDs), r.TagIDs))
	}
	if !eqSet(s.ActorIDs, r.ActorIDs) {
		d = append(d, fmt.Sprintf("actors %v != %v", sorted(s.ActorIDs), r.ActorIDs))
	}
	if imp.cfg.Phases.Markers && s.MarkerCount < e.markers {
		d = append(d, fmt.Sprintf("markers %d < %d", s.MarkerCount, e.markers))
	}
	if imp.v2 && r.Interactions != nil {
		in := r.Interactions
		if in.Rating != nil && math.Abs(s.Rating-*in.Rating) > 0.01 {
			d = append(d, fmt.Sprintf("rating %.1f != %.1f", s.Rating, *in.Rating))
		}
		if in.JizzedCount != nil && s.JizzedCount != *in.JizzedCount {
			d = append(d, fmt.Sprintf("jizzed_count %d != %d", s.JizzedCount, *in.JizzedCount))
		}
		if s.WatchCount < len(in.Watches) {
			d = append(d, fmt.Sprintf("watch history %d < %d", s.WatchCount, len(in.Watches)))
		}
	}
	if imp.v2 && r.ViewCount != nil && s.ViewCount != *r.ViewCount {
		d = append(d, fmt.Sprintf("view_count %d != %d", s.ViewCount, *r.ViewCount))
	}
	return d
}

func eqUintPtr(a, b *uint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func fmtPtr(p *uint) string {
	if p == nil {
		return "none"
	}
	return fmt.Sprint(*p)
}

func sorted(ids []uint) []uint {
	out := append([]uint(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func eqSet(a, b []uint) bool {
	sa, sb := sorted(a), sorted(b)
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
