package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("=== Stash -> GoonHub Importer ===")
	fmt.Println()

	// 1. Load config
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}
	if cfg.DryRun {
		fmt.Println("[Config]  DRY RUN mode enabled - no changes will be made")
	}
	if cfg.SceneLimit > 0 {
		fmt.Printf("[Config]  Scene limit: %d\n", cfg.SceneLimit)
	}
	fmt.Printf("[Config]  Stash:   %s\n", cfg.StashBaseURL)
	fmt.Printf("[Config]  GoonHub: %s\n", cfg.GoonHubBaseURL)

	// 2. Load path mappings
	mappings, err := LoadMappings(cfg.MappingsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Mappings error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Config]  Loaded %d path mapping(s)\n", len(mappings))
	cfg.PathMappings = mappings

	// 3. Load ID map (for resume)
	idMap, err := LoadIDMap(cfg.IDMapFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ID map error: %v\n", err)
		os.Exit(1)
	}
	existing := len(idMap.Tags) + len(idMap.Studios) + len(idMap.Actors) + len(idMap.Scenes) + len(idMap.Markers)
	if existing > 0 {
		fmt.Printf("[Config]  Resuming with %d existing mappings (tags:%d studios:%d actors:%d scenes:%d markers:%d)\n",
			existing, len(idMap.Tags), len(idMap.Studios), len(idMap.Actors), len(idMap.Scenes), len(idMap.Markers))
	}

	// 4. Initialize clients
	stashClient := NewStashClient(cfg.StashBaseURL, cfg.StashAPIKey)
	ghClient := NewGoonHubClient(cfg.GoonHubBaseURL)
	pathMapper := NewPathMapper(cfg.PathMappings)

	// 5. Authenticate with GoonHub
	fmt.Printf("\n[Auth]    Logging in to GoonHub as %q...\n", cfg.GoonHubUsername)
	if err := ghClient.Login(cfg.GoonHubUsername, cfg.GoonHubPassword); err != nil {
		fmt.Fprintf(os.Stderr, "GoonHub login failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[Auth]    Login successful")

	// 6. Initialize importer
	imp := NewImporter(stashClient, ghClient, idMap, pathMapper, cfg)

	// 7. Pre-fetch existing GH entities
	if err := imp.PreFetchExisting(); err != nil {
		fmt.Fprintf(os.Stderr, "Pre-fetch error: %v\n", err)
		os.Exit(1)
	}

	// 8. Fetch all data from Stash
	fmt.Println("\n[Stash]   Fetching data from Stash...")

	stashTags, err := stashClient.FetchTags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch Stash tags: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Stash]   Found %d tags\n", len(stashTags))

	stashStudios, err := stashClient.FetchStudios()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch Stash studios: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Stash]   Found %d studios\n", len(stashStudios))

	stashPerformers, err := stashClient.FetchPerformers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch Stash performers: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Stash]   Found %d performers\n", len(stashPerformers))

	stashScenes, err := stashClient.FetchScenes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch Stash scenes: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Stash]   Found %d scenes\n", len(stashScenes))

	if cfg.SceneLimit > 0 && len(stashScenes) > cfg.SceneLimit {
		fmt.Printf("[Stash]   Limiting to %d scenes (SCENE_LIMIT)\n", cfg.SceneLimit)
		stashScenes = stashScenes[:cfg.SceneLimit]
	}

	// Count total markers
	totalMarkers := 0
	for _, s := range stashScenes {
		totalMarkers += len(s.SceneMarkers)
	}
	fmt.Printf("[Stash]   Found %d scene markers\n", totalMarkers)

	// 9. Run import phases
	allStats := make(map[string]PhaseStats)

	// Phase 1: Tags
	tagStats := imp.ImportTags(stashTags)
	allStats["Tags"] = tagStats
	if !cfg.DryRun {
		if err := idMap.Save(cfg.IDMapFile); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to save id map after tags: %v\n", err)
		}
	}

	// Phase 2: Studios
	studioStats := imp.ImportStudios(stashStudios)
	allStats["Studios"] = studioStats
	if !cfg.DryRun {
		if err := idMap.Save(cfg.IDMapFile); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to save id map after studios: %v\n", err)
		}
	}

	// Phase 3: Performers -> Actors
	actorStats := imp.ImportPerformers(stashPerformers)
	allStats["Actors"] = actorStats
	if !cfg.DryRun {
		if err := idMap.Save(cfg.IDMapFile); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to save id map after actors: %v\n", err)
		}
	}

	// Phase 4: Scenes
	sceneStats := imp.ImportScenes(stashScenes)
	allStats["Scenes"] = sceneStats
	if !cfg.DryRun {
		if err := idMap.Save(cfg.IDMapFile); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to save id map after scenes: %v\n", err)
		}
	}

	// Phase 5: Markers
	markerStats := imp.ImportMarkers(stashScenes)
	allStats["Markers"] = markerStats
	if !cfg.DryRun {
		if err := idMap.Save(cfg.IDMapFile); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to save id map after markers: %v\n", err)
		}
	}

	// 10. Print summary
	fmt.Println("\n=== Import Summary ===")
	totalCreated := 0
	totalSkipped := 0
	totalErrors := 0
	for _, phase := range []string{"Tags", "Studios", "Actors", "Scenes", "Markers"} {
		s := allStats[phase]
		fmt.Printf("  %-10s %d created, %d skipped, %d errors\n", phase+":", s.Created, s.Skipped, s.Errors)
		totalCreated += s.Created
		totalSkipped += s.Skipped
		totalErrors += s.Errors
	}
	fmt.Printf("  %-10s %d created, %d skipped, %d errors\n", "Total:", totalCreated, totalSkipped, totalErrors)

	if !cfg.DryRun && totalCreated > 0 {
		fmt.Printf("\nRemember to rebuild the search index:\n")
		fmt.Printf("  curl -X POST %s/api/v1/admin/search/reindex -H \"Authorization: Bearer <token>\"\n", cfg.GoonHubBaseURL)
	}

	if totalErrors > 0 {
		os.Exit(1)
	}
}
