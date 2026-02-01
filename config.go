package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	StashBaseURL      string
	StashAPIKey       string
	GoonHubBaseURL    string
	GoonHubUsername   string
	GoonHubPassword   string
	MarkerUserID      uint
	SceneLimit        int
	DryRun            bool
	SkipFileCheck     bool
	PathMappings      []PathMapping
	MappingsFile      string
	IDMapFile         string
}

type PathMapping struct {
	StashPrefix   string `json:"stash_prefix"`
	GoonHubPrefix string `json:"goonhub_prefix"`
	StoragePathID uint   `json:"storage_path_id"`
}

type MappingsConfig struct {
	PathMappings []PathMapping `json:"path_mappings"`
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		StashBaseURL:   os.Getenv("STASH_BASE_URL"),
		StashAPIKey:    os.Getenv("STASH_API_KEY"),
		GoonHubBaseURL: os.Getenv("GOONHUB_BASE_URL"),
		GoonHubUsername: os.Getenv("GOONHUB_USERNAME"),
		GoonHubPassword: os.Getenv("GOONHUB_PASSWORD"),
		DryRun:         os.Getenv("DRY_RUN") == "true",
		SkipFileCheck:  os.Getenv("SKIP_FILE_CHECK") == "true",
		MappingsFile:   "mappings.json",
		IDMapFile:      "id_map.json",
	}

	if cfg.StashBaseURL == "" {
		return nil, fmt.Errorf("STASH_BASE_URL is required")
	}
	if cfg.StashAPIKey == "" {
		return nil, fmt.Errorf("STASH_API_KEY is required")
	}
	if cfg.GoonHubBaseURL == "" {
		return nil, fmt.Errorf("GOONHUB_BASE_URL is required")
	}
	if cfg.GoonHubUsername == "" {
		return nil, fmt.Errorf("GOONHUB_USERNAME is required")
	}
	if cfg.GoonHubPassword == "" {
		return nil, fmt.Errorf("GOONHUB_PASSWORD is required")
	}

	markerUserIDStr := os.Getenv("GOONHUB_MARKER_USER_ID")
	if markerUserIDStr == "" {
		return nil, fmt.Errorf("GOONHUB_MARKER_USER_ID is required")
	}
	var markerUserID uint
	if _, err := fmt.Sscanf(markerUserIDStr, "%d", &markerUserID); err != nil {
		return nil, fmt.Errorf("GOONHUB_MARKER_USER_ID must be a positive integer: %w", err)
	}
	cfg.MarkerUserID = markerUserID

	if limitStr := os.Getenv("SCENE_LIMIT"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			return nil, fmt.Errorf("SCENE_LIMIT must be a positive integer: %s", limitStr)
		}
		cfg.SceneLimit = limit
	}

	return cfg, nil
}

func LoadMappings(path string) ([]PathMapping, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("mappings file not found: %s (create it with path_mappings array)", path)
		}
		return nil, fmt.Errorf("failed to read mappings file: %w", err)
	}

	var mappings MappingsConfig
	if err := json.Unmarshal(data, &mappings); err != nil {
		return nil, fmt.Errorf("failed to parse mappings file: %w", err)
	}

	if len(mappings.PathMappings) == 0 {
		return nil, fmt.Errorf("no path_mappings defined in %s", path)
	}

	return mappings.PathMappings, nil
}
