package importer

import (
	"context"
	"fmt"
	"strings"

	"goonhub-stash-importer/internal/config"
	"goonhub-stash-importer/internal/goonhub"
	"goonhub-stash-importer/internal/stash"
	"goonhub-stash-importer/internal/state"
)

// --- Tags ---

func (imp *Importer) importTags(ctx context.Context, tags []stash.Tag) error {
	st := imp.rep.S("tags")
	strategy := imp.cfg.Matching.Tags
	imp.log.Info("importing tags", "count", len(tags))

	existing, err := imp.gh.ListTags(ctx)
	if err != nil {
		return fmt.Errorf("failed to list goonhub tags: %w", err)
	}
	byName := map[string]uint{}
	byCI := map[string]uint{}
	exists := map[uint]bool{}
	index := func(name string, id uint) {
		byName[normName(name, config.MatchName)] = id
		byCI[normName(name, config.MatchNameCI)] = id
		exists[id] = true
	}
	for _, t := range existing {
		index(t.Name, t.ID)
	}
	lookup := func(name string, strat string) (uint, bool) {
		switch strat {
		case config.MatchName:
			id, ok := byName[normName(name, config.MatchName)]
			return id, ok
		case config.MatchNone:
			return 0, false
		}
		id, ok := byCI[normName(name, config.MatchNameCI)]
		return id, ok
	}
	rename := map[string]string{}
	for k, v := range imp.cfg.Tags.Rename {
		rename[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}

	for _, t := range tags {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if imp.excludedTag[t.ID] {
			st.Skipped.Add(1)
			continue
		}
		if deref(t.Description) != "" {
			imp.rep.Drop("tag.description", 1)
		}
		imp.rep.Drop("tag.aliases", len(t.Aliases))
		imp.rep.Drop("tag.parents (hierarchy)", len(t.Parents))

		target := t.Name
		if r, ok := rename[strings.ToLower(strings.TrimSpace(t.Name))]; ok && r != "" {
			target = r
		}
		target = strings.TrimSpace(target)
		if cut, truncated := truncateRunes(target, 100); truncated {
			imp.rep.Warn("tags", t.ID, t.Name, "name", "name truncated to 100 characters")
			target = cut
		}

		e, found := imp.st.Get(state.KindTags, t.ID)
		if found && e.Status == state.StatusOK && exists[e.GH] {
			st.Unchanged.Add(1)
			continue
		}
		if id, ok := lookup(target, strategy); ok {
			imp.st.Put(state.KindTags, t.ID, state.Entry{GH: id, Status: state.StatusOK})
			st.Reused.Add(1)
			continue
		}
		if imp.cfg.DryRun {
			id := imp.fakeID()
			index(target, id)
			imp.st.Put(state.KindTags, t.ID, state.Entry{GH: id, Status: state.StatusOK})
			st.Created.Add(1)
			imp.log.Debug("would create tag", "name", target)
			continue
		}
		created, err := imp.gh.CreateTag(ctx, target, imp.cfg.Tags.Color)
		if _, conflict := goonhub.IsConflict(err); conflict {
			// Created concurrently or exists with another case: resolve by name.
			if fresh, lerr := imp.gh.ListTags(ctx); lerr == nil {
				for _, x := range fresh {
					index(x.Name, x.ID)
				}
			}
			if id, ok := lookup(target, config.MatchNameCI); ok {
				imp.st.Put(state.KindTags, t.ID, state.Entry{GH: id, Status: state.StatusOK})
				st.Reused.Add(1)
				continue
			}
		}
		if err != nil {
			imp.st.Put(state.KindTags, t.ID, state.Entry{Status: state.StatusFailed, Err: err.Error()})
			imp.rep.Error("tags", t.ID, t.Name, "create", err)
			st.Failed.Add(1)
			continue
		}
		index(target, created.ID)
		imp.st.Put(state.KindTags, t.ID, state.Entry{GH: created.ID, Status: state.StatusOK})
		st.Created.Add(1)
		imp.log.Debug("created tag", "name", target, "id", created.ID)
	}
	imp.log.Info("tags done", "stats", st.snapshot())
	return nil
}

// --- Studios ---

func (imp *Importer) studioRequest(s stash.Studio) goonhub.StudioRequest {
	r := goonhub.StudioRequest{Name: strings.TrimSpace(s.Name), Description: deref(s.Details), Rating: ratingStars(s.Rating100)}
	if len(s.URLs) > 0 {
		r.URL = s.URLs[0]
	}
	return r
}

func (imp *Importer) importStudios(ctx context.Context) error {
	st := imp.rep.S("studios")
	studios, err := imp.stash.Studios(ctx, 1000)
	if err != nil {
		return err
	}
	imp.log.Info("importing studios", "count", len(studios))
	existing, err := imp.gh.ListStudios(ctx)
	if err != nil {
		return fmt.Errorf("failed to list goonhub studios: %w", err)
	}
	byName := map[string]uint{}
	exists := map[uint]bool{}
	for _, s := range existing {
		exists[s.ID] = true
		if _, dup := byName[normName(s.Name, imp.cfg.Matching.Studios)]; !dup {
			byName[normName(s.Name, imp.cfg.Matching.Studios)] = s.ID
		}
	}

	touched := map[string]bool{} // studios created/updated in this run (parents to (re)apply)
	for _, s := range studios {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		imp.rep.Drop("studio.aliases", len(s.Aliases))
		imp.rep.Drop("studio.extra_urls", max(len(s.URLs)-1, 0))
		if s.Favorite {
			imp.rep.Drop("studio.favorite", 1)
		}
		if s.ImagePath != nil && !strings.Contains(*s.ImagePath, "default=true") {
			imp.rep.Drop("studio.image", 1)
		}
		req := imp.studioRequest(s)
		hash := hashOf(req)
		e, found := imp.st.Get(state.KindStudios, s.ID)
		if found && e.GH != 0 && !exists[e.GH] && !imp.cfg.DryRun {
			found = false // deleted in GoonHub since: re-resolve
		}
		if !imp.needsWork(e, found, hash) {
			st.Unchanged.Add(1)
			continue
		}
		switch {
		case found && e.GH != 0: // merge: update
			if !imp.cfg.DryRun {
				if err := imp.gh.UpdateStudio(ctx, e.GH, map[string]any{"name": req.Name, "url": req.URL, "description": req.Description, "rating": req.Rating}); err != nil {
					imp.st.Put(state.KindStudios, s.ID, state.Entry{GH: e.GH, Status: state.StatusPartial, Hash: e.Hash, Err: err.Error()})
					imp.rep.Error("studios", s.ID, s.Name, "update", err)
					st.Partial.Add(1)
					continue
				}
			}
			imp.st.Put(state.KindStudios, s.ID, state.Entry{GH: e.GH, Status: state.StatusOK, Hash: hash})
			st.Updated.Add(1)
		default:
			if id, ok := byName[normName(req.Name, imp.cfg.Matching.Studios)]; ok && imp.cfg.Matching.Studios != config.MatchNone {
				imp.st.Put(state.KindStudios, s.ID, state.Entry{GH: id, Status: state.StatusOK, Hash: hash})
				st.Reused.Add(1)
				continue
			}
			var id uint
			if imp.cfg.DryRun {
				id = imp.fakeID()
			} else {
				created, err := imp.gh.CreateStudio(ctx, req)
				if err != nil {
					imp.st.Put(state.KindStudios, s.ID, state.Entry{Status: state.StatusFailed, Err: err.Error()})
					imp.rep.Error("studios", s.ID, s.Name, "create", err)
					st.Failed.Add(1)
					continue
				}
				id = created.ID
			}
			byName[normName(req.Name, imp.cfg.Matching.Studios)] = id
			imp.st.Put(state.KindStudios, s.ID, state.Entry{GH: id, Status: state.StatusOK, Hash: hash})
			st.Created.Add(1)
		}
		touched[s.ID] = true
	}

	// Parents, once every studio has an ID.
	for _, s := range studios {
		if s.ParentStudio == nil || !touched[s.ID] {
			continue
		}
		id, ok := imp.st.GHID(state.KindStudios, s.ID)
		parent, pok := imp.st.GHID(state.KindStudios, s.ParentStudio.ID)
		if !ok {
			continue
		}
		if !pok {
			imp.rep.Warn("studios", s.ID, s.Name, "parent", "parent studio stash:%s is not imported", s.ParentStudio.ID)
			continue
		}
		if imp.cfg.DryRun {
			continue
		}
		if err := imp.gh.UpdateStudio(ctx, id, map[string]any{"parent_id": parent}); err != nil {
			e, _ := imp.st.Get(state.KindStudios, s.ID)
			e.Status, e.Err = state.StatusPartial, "parent: "+err.Error()
			imp.st.Put(state.KindStudios, s.ID, e)
			imp.rep.Error("studios", s.ID, s.Name, "parent", err)
			st.Partial.Add(1)
		}
	}
	imp.log.Info("studios done", "stats", st.snapshot())
	return nil
}

// --- Performers ---

func (imp *Importer) actorName(p stash.Performer) string {
	name := strings.TrimSpace(p.Name)
	if d := deref(p.Disambiguation); d != "" && imp.cfg.Matching.UseDisambiguation {
		name = fmt.Sprintf("%s (%s)", name, d)
	}
	return name
}

func (imp *Importer) actorRequest(p stash.Performer) goonhub.ActorRequest {
	birthday, ok := normalizeDate(p.Birthdate)
	if !ok {
		imp.rep.Warn("performers", p.ID, p.Name, "birthdate", "invalid birthdate %q ignored", deref(p.Birthdate))
	}
	death, ok := normalizeDate(p.DeathDate)
	if !ok {
		imp.rep.Warn("performers", p.ID, p.Name, "death_date", "invalid death date %q ignored", deref(p.DeathDate))
	}
	r := goonhub.ActorRequest{
		Name:            imp.actorName(p),
		Gender:          mapGender(p.Gender, imp.cfg.Performers.GenderMap),
		Birthday:        birthday,
		DateOfDeath:     death,
		Ethnicity:       deref(p.Ethnicity),
		Nationality:     deref(p.Country),
		CareerStartYear: yearOf(p.CareerStart),
		CareerEndYear:   yearOf(p.CareerEnd),
		HeightCm:        p.HeightCm,
		WeightKg:        p.Weight,
		Measurements:    deref(p.Measurements),
		HairColor:       deref(p.HairColor),
		EyeColor:        deref(p.EyeColor),
		Tattoos:         deref(p.Tattoos),
		Piercings:       deref(p.Piercings),
		FakeBoobs:       isFakeBoobs(p.FakeTits),
	}
	if imp.cfg.Performers.ImportAliases && len(p.AliasList) > 0 {
		r.Aliases = p.AliasList
	}
	return r
}

func (imp *Importer) importPerformers(ctx context.Context) error {
	st := imp.rep.S("performers")
	perfs, err := imp.stash.Performers(ctx, 1000)
	if err != nil {
		return err
	}
	imp.log.Info("importing performers", "count", len(perfs))
	existing, err := imp.gh.ListActors(ctx)
	if err != nil {
		return fmt.Errorf("failed to list goonhub actors: %w", err)
	}
	byName := map[string]goonhub.NamedItem{}
	byID := map[uint]goonhub.NamedItem{}
	for _, a := range existing {
		byID[a.ID] = a
		if _, dup := byName[normName(a.Name, imp.cfg.Matching.Performers)]; !dup {
			byName[normName(a.Name, imp.cfg.Matching.Performers)] = a
		}
	}

	for _, p := range perfs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req := imp.actorRequest(p)
		hash := hashOf(req)
		e, found := imp.st.Get(state.KindPerformers, p.ID)
		if found && e.GH != 0 && !imp.cfg.DryRun {
			if _, ok := byID[e.GH]; !ok {
				found = false // deleted in GoonHub since
			}
		}
		if found && e.GH != 0 {
			imp.actorUUID[e.GH] = byID[e.GH].UUID
		}
		if !imp.needsWork(e, found, hash) {
			st.Unchanged.Add(1)
			continue
		}
		var item goonhub.NamedItem
		switch {
		case found && e.GH != 0:
			item = byID[e.GH]
			item.ID = e.GH
			if !imp.cfg.DryRun {
				if err := imp.gh.UpdateActor(ctx, e.GH, req); err != nil {
					imp.st.Put(state.KindPerformers, p.ID, state.Entry{GH: e.GH, Status: state.StatusPartial, Hash: e.Hash, Img: e.Img, Err: err.Error()})
					imp.rep.Error("performers", p.ID, p.Name, "update", err)
					st.Partial.Add(1)
					continue
				}
			}
			st.Updated.Add(1)
		default:
			if a, ok := byName[normName(req.Name, imp.cfg.Matching.Performers)]; ok && imp.cfg.Matching.Performers != config.MatchNone {
				item = a
				st.Reused.Add(1)
			} else if imp.cfg.DryRun {
				item = goonhub.NamedItem{ID: imp.fakeID(), Name: req.Name}
				st.Created.Add(1)
			} else {
				created, err := imp.gh.CreateActor(ctx, req)
				if err != nil {
					imp.st.Put(state.KindPerformers, p.ID, state.Entry{Status: state.StatusFailed, Err: err.Error()})
					imp.rep.Error("performers", p.ID, p.Name, "create", err)
					st.Failed.Add(1)
					continue
				}
				item = created
				st.Created.Add(1)
			}
			byName[normName(req.Name, imp.cfg.Matching.Performers)] = item
			byID[item.ID] = item
		}
		imp.actorUUID[item.ID] = item.UUID
		entry := state.Entry{GH: item.ID, Status: state.StatusOK, Hash: hash, Img: e.Img}
		if err := imp.performerExtras(ctx, p, item, &entry); err != nil {
			entry.Status, entry.Err = state.StatusPartial, err.Error()
			st.Partial.Add(1)
		}
		imp.st.Put(state.KindPerformers, p.ID, entry)
	}
	imp.log.Info("performers done", "stats", st.snapshot())
	return nil
}

// performerExtras uploads the image and applies rating/favorite for the current user.
func (imp *Importer) performerExtras(ctx context.Context, p stash.Performer, a goonhub.NamedItem, e *state.Entry) error {
	var firstErr error
	fail := func(stage string, err error) {
		imp.rep.Error("performers", p.ID, p.Name, stage, err)
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", stage, err)
		}
	}
	img := deref(p.ImagePath)
	if imp.cfg.Images.Performers && img != "" && !strings.Contains(img, "default=true") && e.Img != img {
		is := imp.rep.S("images")
		if imp.cfg.DryRun {
			is.Created.Add(1)
		} else if data, ctype, err := imp.stash.Download(ctx, img); err != nil {
			is.Failed.Add(1)
			fail("image_download", err)
		} else if data != nil {
			if err := imp.gh.UploadActorImage(ctx, a.ID, "performer-"+p.ID+extFor(ctype), data); err != nil {
				is.Failed.Add(1)
				fail("image_upload", err)
			} else {
				e.Img = img
				is.Created.Add(1)
			}
		}
	}
	if !imp.cfg.Phases.Interactions || imp.cfg.DryRun || (p.Rating100 == nil && !p.Favorite) {
		return firstErr
	}
	if a.UUID == "" {
		imp.warnOnceMsg("actor-uuid", "goonhub did not return actor UUIDs: performer rating/favorite skipped")
		return firstErr
	}
	is := imp.rep.S("interactions")
	rating, liked, err := imp.gh.ActorInteractions(ctx, a.UUID)
	if err != nil {
		is.Failed.Add(1)
		fail("interactions", err)
		return firstErr
	}
	if r := ratingStars(p.Rating100); r != nil && imp.cfg.Interactions.Rating && rating != *r {
		if err := imp.gh.SetActorRating(ctx, a.UUID, *r); err != nil {
			is.Failed.Add(1)
			fail("rating", err)
		} else {
			is.Updated.Add(1)
		}
	}
	if p.Favorite && !liked {
		if err := imp.gh.ToggleActorLike(ctx, a.UUID); err != nil {
			is.Failed.Add(1)
			fail("favorite", err)
		} else {
			is.Updated.Add(1)
		}
	}
	return firstErr
}

func extFor(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}
