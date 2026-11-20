// Package config loads the importer configuration from a YAML file, with
// ${VAR} / ${VAR:-default} environment expansion and a legacy fallback on the
// v1 .env + mappings.json format.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// OnExisting strategies.
const (
	OnExistingSkip  = "skip"  // entities already imported are left untouched
	OnExistingMerge = "merge" // entities already imported are re-synced when their source changed
)

// Matching strategies for entities already present in GoonHub (not created by this tool).
const (
	MatchNameCI = "name_ci" // case-insensitive name
	MatchName   = "name"    // exact name
	MatchNone   = "none"    // never reuse, always create
)

type Config struct {
	Stash        StashConfig        `yaml:"stash"`
	GoonHub      GoonHubConfig      `yaml:"goonhub"`
	StateFile    string             `yaml:"state_file"`
	ReportFile   string             `yaml:"report_file"`
	DryRun       bool               `yaml:"dry_run"`
	Concurrency  int                `yaml:"concurrency"`
	OnExisting   string             `yaml:"on_existing"`
	Log          LogConfig          `yaml:"log"`
	Retry        RetryConfig        `yaml:"retry"`
	Phases       PhasesConfig       `yaml:"phases"`
	Matching     MatchingConfig     `yaml:"matching"`
	Tags         TagsConfig         `yaml:"tags"`
	Performers   PerformersConfig   `yaml:"performers"`
	Scenes       ScenesConfig       `yaml:"scenes"`
	Markers      MarkersConfig      `yaml:"markers"`
	Interactions InteractionsConfig `yaml:"interactions"`
	Images       ImagesConfig       `yaml:"images"`
	PathMappings []PathMapping      `yaml:"path_mappings"`
	PostImport   PostImportConfig   `yaml:"post_import"`
}

type StashConfig struct {
	URL      string        `yaml:"url"`
	APIKey   string        `yaml:"api_key"`
	PageSize int           `yaml:"page_size"`
	Timeout  time.Duration `yaml:"timeout"`
}

type GoonHubConfig struct {
	URL      string        `yaml:"url"`
	Username string        `yaml:"username"`
	Password string        `yaml:"password"`
	Timeout  time.Duration `yaml:"timeout"`
	// APIMode: auto (detect v2 via /admin/import/capabilities), v1, v2.
	APIMode string `yaml:"api_mode"`
}

type LogConfig struct {
	Level string `yaml:"level"` // debug | info | warn | error
	// ProgressEvery prints a progress line every N scenes (0 = only per page).
	ProgressEvery int `yaml:"progress_every"`
}

type RetryConfig struct {
	MaxAttempts    int           `yaml:"max_attempts"`
	InitialBackoff time.Duration `yaml:"initial_backoff"`
	MaxBackoff     time.Duration `yaml:"max_backoff"`
}

type PhasesConfig struct {
	Tags         bool `yaml:"tags"`
	Studios      bool `yaml:"studios"`
	Performers   bool `yaml:"performers"`
	Scenes       bool `yaml:"scenes"`
	Markers      bool `yaml:"markers"`
	Interactions bool `yaml:"interactions"`
}

type MatchingConfig struct {
	Tags       string `yaml:"tags"`
	Studios    string `yaml:"studios"`
	Performers string `yaml:"performers"`
	// UseDisambiguation appends " (disambiguation)" to performer names that have one,
	// so two Stash performers with the same name don't get merged.
	UseDisambiguation bool `yaml:"use_disambiguation"`
}

type TagsConfig struct {
	// Rename maps a Stash tag name (case-insensitive) to the GoonHub name. Several
	// Stash tags renamed to the same name are merged.
	Rename map[string]string `yaml:"rename"`
	// Exclude lists Stash tag names (case-insensitive) never imported nor attached.
	Exclude []string `yaml:"exclude"`
	// Color applied to created tags ("" = GoonHub default).
	Color string `yaml:"color"`
}

type PerformersConfig struct {
	ImportAliases bool `yaml:"import_aliases"`
	// GenderMap overrides the Stash gender enum -> GoonHub gender string.
	GenderMap map[string]string `yaml:"gender_map"`
}

type ScenesConfig struct {
	// Filter is passed verbatim as the Stash GraphQL SceneFilterType.
	Filter map[string]any `yaml:"filter"`
	// IDs restricts the import to these Stash scene IDs.
	IDs   []string `yaml:"ids"`
	Limit int      `yaml:"limit"`
	// Sort is the Stash sort field used for pagination (stable order matters for resume).
	Sort          string `yaml:"sort"`
	SkipFileCheck bool   `yaml:"skip_file_check"`
	// TitleFallback when the Stash scene has no title: basename_no_ext | basename.
	TitleFallback string `yaml:"title_fallback"`
	Origin        string `yaml:"origin"`
	Type          string `yaml:"type"`
	// TypeRules sets the GoonHub type from the first matching Stash tag.
	TypeRules         []TypeRule `yaml:"type_rules"`
	PreserveCreatedAt bool       `yaml:"preserve_created_at"`
	// UnmappedPath: error | skip — what to do when no path mapping matches.
	UnmappedPath string `yaml:"unmapped_path"`
}

type TypeRule struct {
	Tag  string `yaml:"tag"`
	Type string `yaml:"type"`
}

type MarkersConfig struct {
	Color string `yaml:"color"`
	// ColorByTag maps a primary tag name (case-insensitive) to a marker color.
	ColorByTag map[string]string `yaml:"color_by_tag"`
	// Tags: primary_and_extra | extra | none — which Stash tags become marker tags.
	Tags string `yaml:"tags"`
}

type InteractionsConfig struct {
	Rating      bool `yaml:"rating"`
	OCounter    bool `yaml:"o_counter"`
	PlayCount   bool `yaml:"play_count"`
	PlayHistory bool `yaml:"play_history"`
	Resume      bool `yaml:"resume"`
}

type ImagesConfig struct {
	Performers  bool `yaml:"performers"`
	SceneCovers bool `yaml:"scene_covers"`
}

type PathMapping struct {
	StashPrefix   string `yaml:"stash_prefix" json:"stash_prefix"`
	GoonHubPrefix string `yaml:"goonhub_prefix" json:"goonhub_prefix"`
	StoragePathID uint   `yaml:"storage_path_id" json:"storage_path_id"`
}

type PostImportConfig struct {
	Reindex bool          `yaml:"reindex"`
	Process ProcessConfig `yaml:"process"`
}

type ProcessConfig struct {
	Enabled bool     `yaml:"enabled"`
	Phases  []string `yaml:"phases"`
	Mode    string   `yaml:"mode"` // missing | all
}

// Default returns a configuration with every default applied.
func Default() *Config {
	return &Config{
		Stash:       StashConfig{URL: "http://localhost:9999", PageSize: 250, Timeout: 120 * time.Second},
		GoonHub:     GoonHubConfig{URL: "http://localhost:8080", Timeout: 60 * time.Second, APIMode: "auto"},
		StateFile:   "import_state.json",
		ReportFile:  "import_report.json",
		Concurrency: 4,
		OnExisting:  OnExistingSkip,
		Log:         LogConfig{Level: "info", ProgressEvery: 500},
		Retry:       RetryConfig{MaxAttempts: 5, InitialBackoff: time.Second, MaxBackoff: 30 * time.Second},
		Phases:      PhasesConfig{Tags: true, Studios: true, Performers: true, Scenes: true, Markers: true, Interactions: true},
		Matching:    MatchingConfig{Tags: MatchNameCI, Studios: MatchNameCI, Performers: MatchNameCI, UseDisambiguation: true},
		Performers:  PerformersConfig{ImportAliases: true},
		Scenes: ScenesConfig{
			Sort:              "id",
			TitleFallback:     "basename_no_ext",
			Origin:            "stash",
			PreserveCreatedAt: true,
			UnmappedPath:      "error",
		},
		Markers:      MarkersConfig{Color: "#FFFFFF", Tags: "primary_and_extra"},
		Interactions: InteractionsConfig{Rating: true, OCounter: true, PlayCount: true, PlayHistory: true, Resume: true},
		Images:       ImagesConfig{Performers: true},
		PostImport:   PostImportConfig{Reindex: true, Process: ProcessConfig{Phases: []string{"metadata", "thumbnail", "sprites"}, Mode: "missing"}},
	}
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expandEnv replaces ${VAR} and ${VAR:-default}. Bare $VAR is left untouched so
// values containing '$' (passwords) survive.
func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		if v, ok := os.LookupEnv(sub[1]); ok && v != "" {
			return v
		}
		return sub[3]
	})
}

// Load reads path. If path doesn't exist and legacy env vars are set, the v1
// format (.env + mappings.json) is used instead.
func Load(path string) (*Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && os.Getenv("STASH_BASE_URL") != "" {
			if err := loadLegacy(cfg); err != nil {
				return nil, err
			}
			return cfg, cfg.Validate()
		}
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}
	dec := yaml.NewDecoder(strings.NewReader(expandEnv(string(raw))))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %s: %w", path, err)
	}
	return cfg, nil
}

func loadLegacy(cfg *Config) error {
	cfg.Stash.URL = os.Getenv("STASH_BASE_URL")
	cfg.Stash.APIKey = os.Getenv("STASH_API_KEY")
	cfg.GoonHub.URL = os.Getenv("GOONHUB_BASE_URL")
	cfg.GoonHub.Username = os.Getenv("GOONHUB_USERNAME")
	cfg.GoonHub.Password = os.Getenv("GOONHUB_PASSWORD")
	cfg.DryRun = os.Getenv("DRY_RUN") == "true"
	cfg.Scenes.SkipFileCheck = os.Getenv("SKIP_FILE_CHECK") == "true"
	if v := os.Getenv("SCENE_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("SCENE_LIMIT must be a positive integer: %s", v)
		}
		cfg.Scenes.Limit = n
	}
	data, err := os.ReadFile("mappings.json")
	if err != nil {
		return fmt.Errorf("legacy mode: failed to read mappings.json: %w", err)
	}
	var m struct {
		PathMappings []PathMapping `json:"path_mappings"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("legacy mode: failed to parse mappings.json: %w", err)
	}
	cfg.PathMappings = m.PathMappings
	return nil
}

// Validate checks the configuration for errors that would only surface mid-import.
func (c *Config) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if c.Stash.URL == "" {
		add("stash.url is required")
	}
	if c.Stash.APIKey == "" {
		add("stash.api_key is required")
	}
	if c.Stash.PageSize < 1 || c.Stash.PageSize > 1000 {
		add("stash.page_size must be between 1 and 1000")
	}
	if c.GoonHub.URL == "" {
		add("goonhub.url is required")
	}
	if c.GoonHub.Username == "" || c.GoonHub.Password == "" {
		add("goonhub.username and goonhub.password are required")
	}
	switch c.GoonHub.APIMode {
	case "auto", "v1", "v2":
	default:
		add("goonhub.api_mode must be auto, v1 or v2")
	}
	if c.Concurrency < 1 || c.Concurrency > 64 {
		add("concurrency must be between 1 and 64")
	}
	switch c.OnExisting {
	case OnExistingSkip, OnExistingMerge:
	default:
		add("on_existing must be skip or merge")
	}
	for name, v := range map[string]string{"tags": c.Matching.Tags, "studios": c.Matching.Studios, "performers": c.Matching.Performers} {
		switch v {
		case MatchNameCI, MatchName, MatchNone:
		default:
			add("matching.%s must be name_ci, name or none", name)
		}
	}
	switch c.Scenes.TitleFallback {
	case "basename_no_ext", "basename":
	default:
		add("scenes.title_fallback must be basename_no_ext or basename")
	}
	switch c.Scenes.UnmappedPath {
	case "error", "skip":
	default:
		add("scenes.unmapped_path must be error or skip")
	}
	switch c.Markers.Tags {
	case "primary_and_extra", "extra", "none":
	default:
		add("markers.tags must be primary_and_extra, extra or none")
	}
	if c.Phases.Scenes && len(c.PathMappings) == 0 {
		add("path_mappings must contain at least one mapping")
	}
	for i, m := range c.PathMappings {
		if m.StashPrefix == "" || m.GoonHubPrefix == "" {
			add("path_mappings[%d]: stash_prefix and goonhub_prefix are required", i)
		}
	}
	if c.Retry.MaxAttempts < 1 {
		add("retry.max_attempts must be >= 1")
	}
	if c.PostImport.Process.Enabled {
		switch c.PostImport.Process.Mode {
		case "missing", "all":
		default:
			add("post_import.process.mode must be missing or all")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
