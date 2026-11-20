// Package importer copies Stash metadata into GoonHub. Stash is only ever read.
package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goonhub-stash-importer/internal/config"
	"goonhub-stash-importer/internal/goonhub"
	"goonhub-stash-importer/internal/pathmap"
	"goonhub-stash-importer/internal/stash"
	"goonhub-stash-importer/internal/state"
)

type Importer struct {
	cfg   *config.Config
	stash *stash.Client
	gh    *goonhub.Client
	st    *state.Store
	pm    *pathmap.Mapper
	rep   *Report
	log   *slog.Logger

	v2     bool
	userID uint
	// markerEnd: the server stores the end of marker ranges (v2 feature "marker_end").
	markerEnd bool
	// jizzHistory: the server stores dated "o" events (v2 feature "jizz_history").
	jizzHistory bool

	// Stash tag ID -> name (for type rules, marker labels/colors) and exclusions.
	tagName     map[string]string
	excludedTag map[string]bool
	// Stash performer ID -> GoonHub actor UUID (actor interactions).
	actorUUID map[uint]string

	dryID       atomic.Uint64
	warnOnce    sync.Map
	scenesTotal atomic.Int64
}

func New(cfg *config.Config, sc *stash.Client, gh *goonhub.Client, st *state.Store, rep *Report, log *slog.Logger) *Importer {
	imp := &Importer{
		cfg: cfg, stash: sc, gh: gh, st: st, rep: rep, log: log,
		pm:          pathmap.New(cfg.PathMappings),
		tagName:     map[string]string{},
		excludedTag: map[string]bool{},
		actorUUID:   map[uint]string{},
	}
	imp.dryID.Store(1_000_000_000)
	return imp
}

// Connect logs in, checks the user is admin and detects the API version.
func (imp *Importer) Connect(ctx context.Context) error {
	v, err := imp.stash.Version(ctx)
	if err != nil {
		return fmt.Errorf("stash unreachable: %w", err)
	}
	imp.log.Info("stash connected", "url", imp.cfg.Stash.URL, "version", v)

	if err := imp.gh.Login(ctx); err != nil {
		return fmt.Errorf("goonhub login failed: %w", err)
	}
	id, role, err := imp.gh.Me(ctx)
	if err != nil {
		return fmt.Errorf("goonhub /auth/me failed: %w", err)
	}
	if role != "admin" {
		return fmt.Errorf("goonhub user %q has role %q, the import endpoints need admin", imp.cfg.GoonHub.Username, role)
	}
	imp.userID = id

	caps, err := imp.gh.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("failed to read import capabilities: %w", err)
	}
	switch imp.cfg.GoonHub.APIMode {
	case "v1":
		imp.v2 = false
	case "v2":
		if caps.Version < 2 {
			return errors.New("goonhub.api_mode is v2 but the server only supports the v1 import API")
		}
		imp.v2 = true
	default:
		imp.v2 = caps.Version >= 2
	}
	imp.markerEnd = imp.v2 && caps.Has("marker_end")
	imp.jizzHistory = imp.v2 && caps.Has("jizz_history")
	imp.rep.APIMode = map[bool]string{true: "v2", false: "v1"}[imp.v2]
	imp.log.Info("goonhub connected", "url", imp.cfg.GoonHub.URL, "user_id", id, "import_api", imp.rep.APIMode)
	if !imp.v2 {
		imp.log.Warn("v1 import API: view counts and watch history can't be imported, scenes are created then linked in several calls (not atomic)")
	}
	return imp.checkStoragePaths(ctx)
}

// checkStoragePaths verifies every mapping targets an existing storage path
// and that goonhub_prefix lives under it.
func (imp *Importer) checkStoragePaths(ctx context.Context) error {
	if !imp.cfg.Phases.Scenes {
		return nil
	}
	sps, err := imp.gh.StoragePaths(ctx)
	if err != nil {
		return fmt.Errorf("failed to list goonhub storage paths: %w", err)
	}
	byID := map[uint]goonhub.StoragePath{}
	for _, sp := range sps {
		byID[sp.ID] = sp
	}
	var errs []string
	for _, m := range imp.cfg.PathMappings {
		if m.StoragePathID == 0 {
			continue
		}
		sp, ok := byID[m.StoragePathID]
		if !ok {
			errs = append(errs, fmt.Sprintf("mapping %s: storage_path_id %d does not exist in GoonHub", m.StashPrefix, m.StoragePathID))
			continue
		}
		root := strings.TrimRight(sp.Path, "/")
		dst := strings.TrimRight(m.GoonHubPrefix, "/")
		if dst != root && !strings.HasPrefix(dst, root+"/") {
			errs = append(errs, fmt.Sprintf("mapping %s: goonhub_prefix %s is not inside storage path %d (%s)", m.StashPrefix, m.GoonHubPrefix, sp.ID, sp.Path))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("path mapping errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// Run executes the enabled phases in dependency order.
func (imp *Importer) Run(ctx context.Context) error {
	tags, err := imp.stash.Tags(ctx, 1000)
	if err != nil {
		return err
	}
	for _, t := range tags {
		imp.tagName[t.ID] = t.Name
		if imp.isExcludedTag(t.Name) {
			imp.excludedTag[t.ID] = true
		}
	}
	steps := []struct {
		on  bool
		run func(context.Context) error
	}{
		{imp.cfg.Phases.Tags, func(ctx context.Context) error { return imp.importTags(ctx, tags) }},
		{imp.cfg.Phases.Studios, imp.importStudios},
		{imp.cfg.Phases.Performers, imp.importPerformers},
		{imp.cfg.Phases.Scenes || imp.cfg.Phases.Markers, imp.importScenes},
	}
	for _, s := range steps {
		if !s.on {
			continue
		}
		if err := s.run(ctx); err != nil {
			return err
		}
		if err := imp.st.Save(); err != nil {
			imp.log.Error("failed to save state", "err", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// PostImport triggers the search reindex and processing jobs.
func (imp *Importer) PostImport(ctx context.Context) {
	if imp.cfg.DryRun {
		return
	}
	if imp.cfg.PostImport.Reindex && imp.rep.Changed() {
		if err := imp.gh.Reindex(ctx); err != nil {
			imp.log.Error("search reindex failed", "err", err)
			imp.rep.Error("scenes", "", "", "reindex", err)
		} else {
			imp.log.Info("search reindex triggered")
		}
	}
	if imp.cfg.PostImport.Process.Enabled {
		imp.SubmitProcessing(ctx, imp.rep.CreatedScenes())
	}
}

// SubmitProcessing submits the configured processing phases (thumbnails,
// sprites...) for the given GoonHub scene IDs, in batches of 1000.
func (imp *Importer) SubmitProcessing(ctx context.Context, ids []uint) {
	p := imp.cfg.PostImport.Process
	if len(ids) == 0 {
		imp.log.Info("no scene to process")
		return
	}
	for _, phase := range p.Phases {
		total := 0
		for i := 0; i < len(ids); i += 1000 {
			n, _, err := imp.gh.BulkPhase(ctx, phase, p.Mode, ids[i:min(i+1000, len(ids))])
			if err != nil {
				imp.log.Error("failed to trigger processing", "phase", phase, "err", err)
				imp.rep.Error("scenes", "", "", "process:"+phase, err)
				break
			}
			total += n
		}
		imp.log.Info("processing jobs submitted", "phase", phase, "jobs", total, "scenes", len(ids))
	}
}

func (imp *Importer) isExcludedTag(name string) bool {
	for _, x := range imp.cfg.Tags.Exclude {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func (imp *Importer) fakeID() uint { return uint(imp.dryID.Add(1)) }

// warnOnceMsg logs a warning only the first time key is seen.
func (imp *Importer) warnOnceMsg(key, msg string, args ...any) {
	if _, loaded := imp.warnOnce.LoadOrStore(key, true); !loaded {
		imp.log.Warn(msg, args...)
	}
}

// needsWork decides whether an entity already in the state must be (re)processed.
func (imp *Importer) needsWork(e state.Entry, found bool, hash string) bool {
	if !found || e.Status != state.StatusOK || e.GH == 0 {
		return true
	}
	return imp.cfg.OnExisting == config.OnExistingMerge && e.Hash != hash
}

// progress prints a throughput line.
type progress struct {
	log   *slog.Logger
	kind  string
	total func() int64
	every int64
	start time.Time
	done  atomic.Int64
}

func newProgress(log *slog.Logger, kind string, total func() int64, every int) *progress {
	return &progress{log: log, kind: kind, total: total, every: int64(max(every, 1)), start: time.Now()}
}

func (p *progress) tick(s *Stats) {
	n := p.done.Add(1)
	total := p.total()
	if n%p.every != 0 && n != total {
		return
	}
	el := time.Since(p.start).Seconds()
	rate := float64(n) / max(el, 0.001)
	eta := time.Duration(float64(total-n)/max(rate, 0.001)) * time.Second
	p.log.Info(fmt.Sprintf("[%s] %d/%d (%.1f%%)", p.kind, n, total, 100*float64(n)/float64(max(total, 1))),
		"created", s.Created.Load(), "updated", s.Updated.Load(), "unchanged", s.Unchanged.Load(),
		"partial", s.Partial.Load(), "failed", s.Failed.Load(), "rate", fmt.Sprintf("%.1f/s", rate), "eta", eta.Round(time.Second))
}
