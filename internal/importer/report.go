package importer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Stats counts outcomes per entity kind.
type Stats struct {
	Created   atomic.Int64 `json:"-"`
	Updated   atomic.Int64 `json:"-"`
	Reused    atomic.Int64 `json:"-"` // matched an entity already present in GoonHub
	Unchanged atomic.Int64 `json:"-"` // already imported, nothing to do
	Skipped   atomic.Int64 `json:"-"` // intentionally not imported (excluded, unmapped with skip, parent missing...)
	Partial   atomic.Int64 `json:"-"`
	Failed    atomic.Int64 `json:"-"`
}

func (s *Stats) snapshot() map[string]int64 {
	return map[string]int64{
		"created": s.Created.Load(), "updated": s.Updated.Load(), "reused": s.Reused.Load(),
		"unchanged": s.Unchanged.Load(), "skipped": s.Skipped.Load(), "partial": s.Partial.Load(), "failed": s.Failed.Load(),
	}
}

// Issue is one problem recorded in the report.
type Issue struct {
	Kind     string `json:"kind"`
	StashID  string `json:"stash_id,omitempty"`
	GHID     uint   `json:"goonhub_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Severity string `json:"severity"` // error | warning
	Stage    string `json:"stage"`
	Message  string `json:"message"`
}

type Report struct {
	mu        sync.Mutex
	StartedAt time.Time
	DryRun    bool
	APIMode   string
	stats     map[string]*Stats
	issues    []Issue
	// dropped counts source data GoonHub cannot store (field -> number of entities).
	dropped map[string]int
	// createdScenes lists GoonHub scene IDs created in this run (post-processing).
	createdScenes []uint
}

func NewReport(dryRun bool) *Report {
	r := &Report{StartedAt: time.Now(), DryRun: dryRun, stats: map[string]*Stats{}, dropped: map[string]int{}}
	for _, k := range []string{"tags", "studios", "performers", "scenes", "markers", "images", "interactions"} {
		r.stats[k] = &Stats{}
	}
	return r
}

func (r *Report) S(kind string) *Stats { return r.stats[kind] }

func (r *Report) Add(i Issue) {
	r.mu.Lock()
	r.issues = append(r.issues, i)
	r.mu.Unlock()
}

func (r *Report) Error(kind, stashID, name, stage string, err error) {
	r.Add(Issue{Kind: kind, StashID: stashID, Name: name, Severity: "error", Stage: stage, Message: err.Error()})
}

func (r *Report) Warn(kind, stashID, name, stage, format string, a ...any) {
	r.Add(Issue{Kind: kind, StashID: stashID, Name: name, Severity: "warning", Stage: stage, Message: fmt.Sprintf(format, a...)})
}

func (r *Report) Drop(field string, n int) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	r.dropped[field] += n
	r.mu.Unlock()
}

func (r *Report) SceneCreated(id uint) {
	r.mu.Lock()
	r.createdScenes = append(r.createdScenes, id)
	r.mu.Unlock()
}

func (r *Report) CreatedScenes() []uint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint(nil), r.createdScenes...)
}

func (r *Report) Counts() (errors, warnings int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, i := range r.issues {
		if i.Severity == "error" {
			errors++
		} else {
			warnings++
		}
	}
	return
}

// Changed reports whether anything was written to GoonHub.
func (r *Report) Changed() bool {
	for _, s := range r.stats {
		if s.Created.Load()+s.Updated.Load() > 0 {
			return true
		}
	}
	return false
}

var kindOrder = []string{"tags", "studios", "performers", "scenes", "markers", "images", "interactions"}

func (r *Report) PrintSummary(w io.Writer) {
	fmt.Fprintf(w, "\n=== Summary (%s, api %s) ===\n", time.Since(r.StartedAt).Round(time.Second), r.APIMode)
	if r.DryRun {
		fmt.Fprintln(w, "DRY RUN: nothing was written. 'created'/'updated' = would be created/updated.")
	}
	fmt.Fprintf(w, "  %-13s %8s %8s %8s %9s %8s %8s %8s\n", "", "created", "updated", "reused", "unchanged", "skipped", "partial", "failed")
	for _, k := range kindOrder {
		s := r.stats[k].snapshot()
		fmt.Fprintf(w, "  %-13s %8d %8d %8d %9d %8d %8d %8d\n", k, s["created"], s["updated"], s["reused"], s["unchanged"], s["skipped"], s["partial"], s["failed"])
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.dropped) > 0 {
		fmt.Fprintln(w, "\n  Stash data GoonHub cannot store (not imported):")
		keys := make([]string, 0, len(r.dropped))
		for k := range r.dropped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "    %-55s %d\n", k, r.dropped[k])
		}
	}
	byStage := map[string]int{}
	errs := 0
	for _, i := range r.issues {
		if i.Severity == "error" {
			errs++
			byStage[i.Kind+"/"+i.Stage]++
		}
	}
	fmt.Fprintf(w, "\n  Issues: %d errors, %d warnings\n", errs, len(r.issues)-errs)
	stages := make([]string, 0, len(byStage))
	for k := range byStage {
		stages = append(stages, k)
	}
	sort.Strings(stages)
	for _, k := range stages {
		fmt.Fprintf(w, "    %-40s %d errors\n", k, byStage[k])
	}
	shown := 0
	for _, i := range r.issues {
		if i.Severity != "error" {
			continue
		}
		if shown == 10 {
			fmt.Fprintln(w, "    ... (see the report file for the full list)")
			break
		}
		fmt.Fprintf(w, "    - [%s/%s] stash:%s %q: %s\n", i.Kind, i.Stage, i.StashID, i.Name, i.Message)
		shown++
	}
}

func (r *Report) WriteFile(path string) error {
	if path == "" {
		return nil
	}
	r.mu.Lock()
	stats := map[string]map[string]int64{}
	for k, s := range r.stats {
		stats[k] = s.snapshot()
	}
	out := map[string]any{
		"started_at":  r.StartedAt.UTC(),
		"finished_at": time.Now().UTC(),
		"dry_run":     r.DryRun,
		"api_mode":    r.APIMode,
		"stats":       stats,
		"dropped":     r.dropped,
		"issues":      r.issues,
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
