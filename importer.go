package main

import (
	"fmt"
	"math"
	"strings"
)

type Importer struct {
	stash      *StashClient
	gh         *GoonHubClient
	idMap      *IDMap
	pathMapper *PathMapper
	cfg        *Config

	// Pre-fetched GH entities for name dedup
	ghTags    map[string]uint // name -> id
	ghStudios map[string]uint // name -> id
	ghActors  map[string]uint // name -> id
}

type PhaseStats struct {
	Created int
	Skipped int
	Errors  int
}

func NewImporter(stash *StashClient, gh *GoonHubClient, idMap *IDMap, pathMapper *PathMapper, cfg *Config) *Importer {
	return &Importer{
		stash:      stash,
		gh:         gh,
		idMap:      idMap,
		pathMapper: pathMapper,
		cfg:        cfg,
		ghTags:     make(map[string]uint),
		ghStudios:  make(map[string]uint),
		ghActors:   make(map[string]uint),
	}
}

// PreFetchExisting loads existing GH entities for name-based deduplication.
func (imp *Importer) PreFetchExisting() error {
	fmt.Println("[Setup]  Pre-fetching existing GoonHub entities...")

	tags, err := imp.gh.ListTags()
	if err != nil {
		return fmt.Errorf("failed to fetch existing tags: %w", err)
	}
	for _, t := range tags {
		imp.ghTags[strings.ToLower(t.Name)] = t.ID
	}
	fmt.Printf("[Setup]  Found %d existing tags\n", len(tags))

	studios, err := imp.gh.ListStudios()
	if err != nil {
		return fmt.Errorf("failed to fetch existing studios: %w", err)
	}
	for _, s := range studios {
		imp.ghStudios[strings.ToLower(s.Name)] = s.ID
	}
	fmt.Printf("[Setup]  Found %d existing studios\n", len(studios))

	actors, err := imp.gh.ListActors()
	if err != nil {
		return fmt.Errorf("failed to fetch existing actors: %w", err)
	}
	for _, a := range actors {
		imp.ghActors[strings.ToLower(a.Name)] = a.ID
	}
	fmt.Printf("[Setup]  Found %d existing actors\n", len(actors))

	return nil
}

// Phase 1: Import Tags
func (imp *Importer) ImportTags(stashTags []StashTag) PhaseStats {
	stats := PhaseStats{}
	total := len(stashTags)
	fmt.Printf("\n[Tags]    Importing %d tags...\n", total)

	for i, tag := range stashTags {
		idx := fmt.Sprintf("[%*d/%d]", digits(total), i+1, total)

		// Already mapped
		if _, ok := imp.idMap.Tags[tag.ID]; ok {
			fmt.Printf("[Tags]    %s Skipped %q (already mapped)\n", idx, tag.Name)
			stats.Skipped++
			continue
		}

		// Check existing by name
		if ghID, ok := imp.ghTags[strings.ToLower(tag.Name)]; ok {
			imp.idMap.Tags[tag.ID] = ghID
			fmt.Printf("[Tags]    %s Reused %q (existing gh:%d)\n", idx, tag.Name, ghID)
			stats.Skipped++
			continue
		}

		if imp.cfg.DryRun {
			fmt.Printf("[Tags]    %s [DRY RUN] Would create %q\n", idx, tag.Name)
			stats.Created++
			continue
		}

		created, err := imp.gh.CreateTag(tag.Name, "")
		if err != nil {
			if isConflict(err) {
				fmt.Printf("[Tags]    %s Skipped %q (conflict/already exists)\n", idx, tag.Name)
				stats.Skipped++
				continue
			}
			fmt.Printf("[Tags]    %s ERROR creating %q: %v\n", idx, tag.Name, err)
			stats.Errors++
			continue
		}

		imp.idMap.Tags[tag.ID] = created.ID
		imp.ghTags[strings.ToLower(tag.Name)] = created.ID
		fmt.Printf("[Tags]    %s Created %q (stash:%s -> gh:%d)\n", idx, tag.Name, tag.ID, created.ID)
		stats.Created++
	}

	printStats("Tags", stats)
	return stats
}

// Phase 2: Import Studios
func (imp *Importer) ImportStudios(stashStudios []StashStudio) PhaseStats {
	stats := PhaseStats{}
	total := len(stashStudios)
	fmt.Printf("\n[Studios] Importing %d studios...\n", total)

	// First pass: create all studios without parent
	for i, studio := range stashStudios {
		idx := fmt.Sprintf("[%*d/%d]", digits(total), i+1, total)

		if _, ok := imp.idMap.Studios[studio.ID]; ok {
			fmt.Printf("[Studios] %s Skipped %q (already mapped)\n", idx, studio.Name)
			stats.Skipped++
			continue
		}

		if ghID, ok := imp.ghStudios[strings.ToLower(studio.Name)]; ok {
			imp.idMap.Studios[studio.ID] = ghID
			fmt.Printf("[Studios] %s Reused %q (existing gh:%d)\n", idx, studio.Name, ghID)
			stats.Skipped++
			continue
		}

		if imp.cfg.DryRun {
			fmt.Printf("[Studios] %s [DRY RUN] Would create %q\n", idx, studio.Name)
			stats.Created++
			continue
		}

		req := GHCreateStudioRequest{
			Name:        studio.Name,
			Description: studio.Details,
		}
		if len(studio.URLs) > 0 {
			req.URL = studio.URLs[0]
		}
		if studio.Rating100 != nil {
			r := float64(*studio.Rating100) / 20.0
			req.Rating = &r
		}

		created, err := imp.gh.CreateStudio(req)
		if err != nil {
			if isConflict(err) {
				fmt.Printf("[Studios] %s Skipped %q (conflict/already exists)\n", idx, studio.Name)
				stats.Skipped++
				continue
			}
			fmt.Printf("[Studios] %s ERROR creating %q: %v\n", idx, studio.Name, err)
			stats.Errors++
			continue
		}

		imp.idMap.Studios[studio.ID] = created.ID
		imp.ghStudios[strings.ToLower(studio.Name)] = created.ID
		fmt.Printf("[Studios] %s Created %q (stash:%s -> gh:%d)\n", idx, studio.Name, studio.ID, created.ID)
		stats.Created++
	}

	// Second pass: set parent relationships
	parentCount := 0
	for _, studio := range stashStudios {
		if studio.ParentStudio == nil {
			continue
		}

		ghID, ok := imp.idMap.Studios[studio.ID]
		if !ok {
			continue
		}
		parentGHID, ok := imp.idMap.Studios[studio.ParentStudio.ID]
		if !ok {
			fmt.Printf("[Studios] WARNING: parent studio stash:%s not mapped for %q\n", studio.ParentStudio.ID, studio.Name)
			continue
		}

		if imp.cfg.DryRun {
			fmt.Printf("[Studios] [DRY RUN] Would set parent of %q to gh:%d\n", studio.Name, parentGHID)
			parentCount++
			continue
		}

		if err := imp.gh.UpdateStudio(ghID, GHUpdateStudioRequest{ParentID: &parentGHID}); err != nil {
			fmt.Printf("[Studios] WARNING: failed to set parent for %q: %v\n", studio.Name, err)
			continue
		}
		parentCount++
	}
	if parentCount > 0 {
		fmt.Printf("[Studios] Set %d parent relationships\n", parentCount)
	}

	printStats("Studios", stats)
	return stats
}

// Phase 3: Import Performers -> Actors
func (imp *Importer) ImportPerformers(stashPerformers []StashPerformer) PhaseStats {
	stats := PhaseStats{}
	total := len(stashPerformers)
	fmt.Printf("\n[Actors]  Importing %d performers...\n", total)

	for i, perf := range stashPerformers {
		idx := fmt.Sprintf("[%*d/%d]", digits(total), i+1, total)

		if _, ok := imp.idMap.Actors[perf.ID]; ok {
			fmt.Printf("[Actors]  %s Skipped %q (already mapped)\n", idx, perf.Name)
			stats.Skipped++
			continue
		}

		if ghID, ok := imp.ghActors[strings.ToLower(perf.Name)]; ok {
			imp.idMap.Actors[perf.ID] = ghID
			fmt.Printf("[Actors]  %s Reused %q (existing gh:%d)\n", idx, perf.Name, ghID)
			stats.Skipped++
			continue
		}

		if imp.cfg.DryRun {
			fmt.Printf("[Actors]  %s [DRY RUN] Would create %q\n", idx, perf.Name)
			stats.Created++
			continue
		}

		req := GHCreateActorRequest{
			Name:         perf.Name,
			Gender:       mapGender(perf.Gender),
			Ethnicity:    derefStr(perf.Ethnicity),
			Nationality:  derefStr(perf.Country),
			HeightCm:     perf.HeightCm,
			Measurements: derefStr(perf.Measurements),
			HairColor:    derefStr(perf.HairColor),
			EyeColor:     derefStr(perf.EyeColor),
			Tattoos:      derefStr(perf.Tattoos),
			Piercings:    derefStr(perf.Piercings),
			FakeBoobs:    isFakeBoobs(perf.FakeTits),
			Birthday:     perf.Birthdate,
			DateOfDeath:  perf.DeathDate,
		}
		if perf.Weight != nil {
			req.WeightKg = perf.Weight
		}

		created, err := imp.gh.CreateActor(req)
		if err != nil {
			if isConflict(err) {
				fmt.Printf("[Actors]  %s Skipped %q (conflict/already exists)\n", idx, perf.Name)
				stats.Skipped++
				continue
			}
			fmt.Printf("[Actors]  %s ERROR creating %q: %v\n", idx, perf.Name, err)
			stats.Errors++
			continue
		}

		imp.idMap.Actors[perf.ID] = created.ID
		imp.ghActors[strings.ToLower(perf.Name)] = created.ID
		fmt.Printf("[Actors]  %s Created %q (stash:%s -> gh:%d)\n", idx, perf.Name, perf.ID, created.ID)
		stats.Created++
	}

	printStats("Actors", stats)
	return stats
}

// Phase 4: Import Scenes
func (imp *Importer) ImportScenes(stashScenes []StashScene) PhaseStats {
	stats := PhaseStats{}
	total := len(stashScenes)
	fmt.Printf("\n[Scenes]  Importing %d scenes...\n", total)

	for i, scene := range stashScenes {
		idx := fmt.Sprintf("[%*d/%d]", digits(total), i+1, total)

		if _, ok := imp.idMap.Scenes[scene.ID]; ok {
			fmt.Printf("[Scenes]  %s Skipped scene %s (already mapped)\n", idx, scene.ID)
			stats.Skipped++
			continue
		}

		if len(scene.Files) == 0 {
			fmt.Printf("[Scenes]  %s WARNING: scene %s has no files, skipping\n", idx, scene.ID)
			stats.Errors++
			continue
		}

		file := scene.Files[0]

		mapped, err := imp.pathMapper.MapPath(file.Path)
		if err != nil {
			fmt.Printf("[Scenes]  %s WARNING: %v, skipping scene %s\n", idx, err, scene.ID)
			stats.Errors++
			continue
		}

		title := derefStr(scene.Title)
		if title == "" {
			title = file.Basename
		}

		if imp.cfg.DryRun {
			fmt.Printf("[Scenes]  %s [DRY RUN] Would import %q (%s)\n", idx, title, file.Path)
			stats.Created++
			continue
		}

		req := GHImportSceneRequest{
			Title:            title,
			StoredPath:       mapped.GoonHubPath,
			OriginalFilename: file.Basename,
			Size:             file.Size,
			Duration:         int(math.Round(file.Duration)),
			Width:            file.Width,
			Height:           file.Height,
			FrameRate:        file.FrameRate,
			BitRate:          file.BitRate,
			VideoCodec:       file.VideoCodec,
			AudioCodec:       file.AudioCodec,
			Description:      derefStr(scene.Details),
			ReleaseDate:      scene.Date,
			Origin:           "stash",
			SkipFileCheck:    imp.cfg.SkipFileCheck,
		}

		if mapped.StoragePathID > 0 {
			spID := mapped.StoragePathID
			req.StoragePathID = &spID
		}

		// Map studio
		if scene.Studio != nil {
			if ghStudioID, ok := imp.idMap.Studios[scene.Studio.ID]; ok {
				req.StudioID = &ghStudioID
			}
		}

		created, err := imp.gh.ImportScene(req)
		if err != nil {
			if conflictErr, ok := err.(*ConflictError); ok {
				if conflictErr.ExistingID > 0 {
					imp.idMap.Scenes[scene.ID] = conflictErr.ExistingID
					fmt.Printf("[Scenes]  %s Skipped %q (already exists as gh:%d)\n", idx, title, conflictErr.ExistingID)
				} else {
					fmt.Printf("[Scenes]  %s Skipped %q (conflict/already exists)\n", idx, title)
				}
				stats.Skipped++
				continue
			}
			fmt.Printf("[Scenes]  %s ERROR importing %q: %v\n", idx, title, err)
			stats.Errors++
			continue
		}

		imp.idMap.Scenes[scene.ID] = created.ID
		fmt.Printf("[Scenes]  %s Created %q (stash:%s -> gh:%d)\n", idx, title, scene.ID, created.ID)
		stats.Created++

		// Set tags
		tagIDs := imp.mapTagIDs(scene.Tags)
		if len(tagIDs) > 0 {
			if err := imp.gh.SetSceneTags(created.ID, tagIDs); err != nil {
				fmt.Printf("[Scenes]  %s WARNING: failed to set tags: %v\n", idx, err)
			}
		}

		// Set actors
		actorIDs := imp.mapActorIDs(scene.Performers)
		if len(actorIDs) > 0 {
			if err := imp.gh.SetSceneActors(created.ID, actorIDs); err != nil {
				fmt.Printf("[Scenes]  %s WARNING: failed to set actors: %v\n", idx, err)
			}
		}
	}

	printStats("Scenes", stats)
	return stats
}

// Phase 5: Import Markers
func (imp *Importer) ImportMarkers(stashScenes []StashScene) PhaseStats {
	stats := PhaseStats{}

	// Count total markers
	totalMarkers := 0
	for _, scene := range stashScenes {
		totalMarkers += len(scene.SceneMarkers)
	}
	fmt.Printf("\n[Markers] Importing %d markers...\n", totalMarkers)

	markerNum := 0
	for _, scene := range stashScenes {
		ghSceneID, ok := imp.idMap.Scenes[scene.ID]
		if !ok {
			// Scene wasn't imported, skip its markers
			markerNum += len(scene.SceneMarkers)
			stats.Skipped += len(scene.SceneMarkers)
			continue
		}

		for _, marker := range scene.SceneMarkers {
			markerNum++
			idx := fmt.Sprintf("[%*d/%d]", digits(totalMarkers), markerNum, totalMarkers)

			if _, ok := imp.idMap.Markers[marker.ID]; ok {
				fmt.Printf("[Markers] %s Skipped marker %s (already mapped)\n", idx, marker.ID)
				stats.Skipped++
				continue
			}

			if imp.cfg.DryRun {
				fmt.Printf("[Markers] %s [DRY RUN] Would import marker %q at %ds\n", idx, marker.Title, int(marker.Seconds))
				stats.Created++
				continue
			}

			created, err := imp.gh.ImportMarker(GHImportMarkerRequest{
				SceneID:   ghSceneID,
				UserID:    imp.cfg.MarkerUserID,
				Timestamp: int(marker.Seconds),
				Label:     marker.Title,
				Color:     "#FFFFFF",
			})
			if err != nil {
				if isConflict(err) {
					stats.Skipped++
					continue
				}
				fmt.Printf("[Markers] %s ERROR importing marker %s: %v\n", idx, marker.ID, err)
				stats.Errors++
				continue
			}

			imp.idMap.Markers[marker.ID] = created.ID
			fmt.Printf("[Markers] %s Created marker %q at %ds (stash:%s -> gh:%d)\n", idx, marker.Title, int(marker.Seconds), marker.ID, created.ID)
			stats.Created++

			// Collect tags: primary_tag + additional tags
			var markerTagIDs []uint
			if marker.PrimaryTag != nil {
				if ghTagID, ok := imp.idMap.Tags[marker.PrimaryTag.ID]; ok {
					markerTagIDs = append(markerTagIDs, ghTagID)
				}
			}
			for _, t := range marker.Tags {
				if ghTagID, ok := imp.idMap.Tags[t.ID]; ok {
					markerTagIDs = append(markerTagIDs, ghTagID)
				}
			}

			if len(markerTagIDs) > 0 {
				if err := imp.gh.SetMarkerTags(created.ID, markerTagIDs); err != nil {
					fmt.Printf("[Markers] %s WARNING: failed to set marker tags: %v\n", idx, err)
				}
			}
		}
	}

	printStats("Markers", stats)
	return stats
}

// --- Helpers ---

func (imp *Importer) mapTagIDs(refs []StashIDRef) []uint {
	var ids []uint
	for _, ref := range refs {
		if ghID, ok := imp.idMap.Tags[ref.ID]; ok {
			ids = append(ids, ghID)
		}
	}
	return ids
}

func (imp *Importer) mapActorIDs(refs []StashIDRef) []uint {
	var ids []uint
	for _, ref := range refs {
		if ghID, ok := imp.idMap.Actors[ref.ID]; ok {
			ids = append(ids, ghID)
		}
	}
	return ids
}

func mapGender(g *string) string {
	if g == nil {
		return ""
	}
	switch strings.ToUpper(*g) {
	case "MALE":
		return "male"
	case "FEMALE":
		return "female"
	case "TRANSGENDER_MALE":
		return "transgender_male"
	case "TRANSGENDER_FEMALE":
		return "transgender_female"
	case "INTERSEX":
		return "intersex"
	case "NON_BINARY":
		return "non_binary"
	default:
		return strings.ToLower(*g)
	}
}

func isFakeBoobs(fakeTits *string) bool {
	if fakeTits == nil {
		return false
	}
	v := strings.ToLower(*fakeTits)
	return v != "" && v != "natural"
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isConflict(err error) bool {
	_, ok := err.(*ConflictError)
	return ok
}

func digits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

func printStats(phase string, stats PhaseStats) {
	fmt.Printf("[%s] Done: %d created, %d skipped, %d errors\n",
		phase, stats.Created, stats.Skipped, stats.Errors)
}
