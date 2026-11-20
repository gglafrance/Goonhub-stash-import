// Package state persists the Stash ID -> GoonHub ID mapping plus a per-entity
// status, so that re-runs resume exactly where the previous run stopped and
// retry only what is incomplete.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	KindTags       = "tags"
	KindStudios    = "studios"
	KindPerformers = "performers"
	KindScenes     = "scenes"
	KindMarkers    = "markers"
)

var Kinds = []string{KindTags, KindStudios, KindPerformers, KindScenes, KindMarkers}

const (
	StatusOK      = "ok"      // fully imported
	StatusPartial = "partial" // entity exists in GoonHub but something attached to it failed
	StatusFailed  = "failed"  // entity could not be created
)

type Entry struct {
	GH     uint   `json:"gh,omitempty"`
	Status string `json:"s"`
	// Hash of the last payload successfully applied (merge mode skips unchanged entities).
	Hash string `json:"h,omitempty"`
	// Img is the source image URL last uploaded (avoids re-uploading).
	Img string `json:"img,omitempty"`
	Err string `json:"e,omitempty"`
}

type file struct {
	Version    int                          `json:"version"`
	GoonHubURL string                       `json:"goonhub_url"`
	UpdatedAt  time.Time                    `json:"updated_at"`
	Entries    map[string]map[string]*Entry `json:"entries"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	data  file
	dirty bool
	ro    bool // dry-run: never written
}

// Open loads path (or a legacy id_map.json) and checks it belongs to goonhubURL.
func Open(path, goonhubURL string, readOnly bool) (*Store, error) {
	s := &Store{path: path, ro: readOnly, data: file{Version: 2, GoonHubURL: goonhubURL, Entries: map[string]map[string]*Entry{}}}
	for _, k := range Kinds {
		s.data.Entries[k] = map[string]*Entry{}
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if legacy, lerr := os.ReadFile(filepath.Join(filepath.Dir(path), "id_map.json")); lerr == nil {
			if err := s.migrateLegacy(legacy); err != nil {
				return nil, err
			}
			s.dirty = true
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state %s: %w", path, err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("failed to parse state %s: %w", path, err)
	}
	if f.GoonHubURL != "" && f.GoonHubURL != goonhubURL {
		return nil, fmt.Errorf("state file %s belongs to GoonHub %s, not %s: use another state_file", path, f.GoonHubURL, goonhubURL)
	}
	for _, k := range Kinds {
		if f.Entries[k] != nil {
			s.data.Entries[k] = f.Entries[k]
		}
	}
	return s, nil
}

// migrateLegacy imports the v1 id_map.json ({"tags": {"1": 5}, "actors": ...}).
// Legacy scenes are marked partial so their associations are re-applied.
func (s *Store) migrateLegacy(raw []byte) error {
	var legacy map[string]map[string]uint
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("failed to parse legacy id_map.json: %w", err)
	}
	rename := map[string]string{"actors": KindPerformers}
	for k, m := range legacy {
		kind := k
		if r, ok := rename[k]; ok {
			kind = r
		}
		if s.data.Entries[kind] == nil {
			continue
		}
		status := StatusOK
		if kind == KindScenes {
			status = StatusPartial
		}
		for id, gh := range m {
			s.data.Entries[kind][id] = &Entry{GH: gh, Status: status}
		}
	}
	return nil
}

func (s *Store) Get(kind, id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Entries[kind][id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// GHID returns the GoonHub ID of an entity that exists in GoonHub.
func (s *Store) GHID(kind, id string) (uint, bool) {
	e, ok := s.Get(kind, id)
	return e.GH, ok && e.GH != 0
}

func (s *Store) Put(kind, id string, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := e
	s.data.Entries[kind][id] = &cp
	s.dirty = true
}

func (s *Store) Delete(kind, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Entries[kind], id)
	s.dirty = true
}

// Counts returns per-status counts for kind.
func (s *Store) Counts(kind string) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, e := range s.data.Entries[kind] {
		out[e.Status]++
	}
	return out
}

// Each calls fn for every entry of kind, in ascending ID order (snapshot).
func (s *Store) Each(kind string, fn func(id string, e Entry)) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.data.Entries[kind]))
	snap := make(map[string]Entry, len(s.data.Entries[kind]))
	for id, e := range s.data.Entries[kind] {
		ids = append(ids, id)
		snap[id] = *e
	}
	s.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool {
		if len(ids[i]) != len(ids[j]) {
			return len(ids[i]) < len(ids[j])
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		fn(id, snap[id])
	}
}

// Save writes atomically (temp file + rename) when something changed.
func (s *Store) Save() error {
	if s.ro {
		return nil
	}
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	s.data.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(s.data)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		s.markDirty()
		return fmt.Errorf("failed to write state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		s.markDirty()
		return fmt.Errorf("failed to replace state: %w", err)
	}
	return nil
}

func (s *Store) markDirty() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

// AutoSave saves every interval until ctx is done, then saves one last time.
func (s *Store) AutoSave(ctx context.Context, interval time.Duration, onErr func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := s.Save(); err != nil {
				onErr(err)
			}
			return
		case <-t.C:
			if err := s.Save(); err != nil {
				onErr(err)
			}
		}
	}
}
