package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goonhub-stash-importer/internal/config"
	"goonhub-stash-importer/internal/goonhub"
	"goonhub-stash-importer/internal/httpx"
	"goonhub-stash-importer/internal/stash"
	"goonhub-stash-importer/internal/state"
)

// --- fake Stash -------------------------------------------------------------

func fakeStash(t *testing.T, scenes []map[string]any) *httptest.Server {
	tags := []map[string]any{{"id": "1", "name": "Anal", "aliases": []string{}, "parents": []any{}}, {"id": "2", "name": "Blowjob", "aliases": []string{}, "parents": []any{}}}
	studios := []map[string]any{{"id": "10", "name": "Studio A", "urls": []string{"https://a"}, "aliases": []string{}}, {"id": "11", "name": "Sub", "urls": []string{}, "aliases": []string{}, "parent_studio": map[string]any{"id": "10"}}}
	perfs := []map[string]any{{"id": "20", "name": "Alice", "alias_list": []string{"Al"}, "gender": "FEMALE", "birthdate": "1990", "rating100": 80, "favorite": true, "image_path": "http://stash/performer/20/image?t=1&default=true"}}
	page := func(items []map[string]any, vars map[string]any) ([]map[string]any, int) {
		f, _ := vars["filter"].(map[string]any)
		p, pp := int(f["page"].(float64)), int(f["per_page"].(float64))
		if ids, ok := vars["ids"].([]any); ok {
			want := map[string]bool{}
			for _, id := range ids {
				want[id.(string)] = true
			}
			var sel []map[string]any
			for _, it := range items {
				if want[it["id"].(string)] {
					sel = append(sel, it)
				}
			}
			items = sel
		}
		lo, hi := min((p-1)*pp, len(items)), min(p*pp, len(items))
		return items[lo:hi], len(items)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(body.Query, "mutation") {
			t.Errorf("a mutation was sent to Stash: %s", body.Query)
		}
		var data any
		switch {
		case strings.Contains(body.Query, "version"):
			data = map[string]any{"version": map[string]any{"version": "test"}}
		case strings.Contains(body.Query, "findTags"):
			l, n := page(tags, body.Variables)
			data = map[string]any{"findTags": map[string]any{"count": n, "tags": l}}
		case strings.Contains(body.Query, "findStudios"):
			l, n := page(studios, body.Variables)
			data = map[string]any{"findStudios": map[string]any{"count": n, "studios": l}}
		case strings.Contains(body.Query, "findPerformers"):
			l, n := page(perfs, body.Variables)
			data = map[string]any{"findPerformers": map[string]any{"count": n, "performers": l}}
		case strings.Contains(body.Query, "findScenes"):
			l, n := page(scenes, body.Variables)
			data = map[string]any{"findScenes": map[string]any{"count": n, "scenes": l}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func stashScene(id int) map[string]any {
	return map[string]any{
		"id": strconv.Itoa(id), "title": fmt.Sprintf("Scene %d", id), "date": "2021-03", "rating100": 90, "o_counter": 2,
		"play_count": 3, "play_history": []string{"2024-01-01T10:00:00Z", "2024-02-01T10:00:00Z"}, "resume_time": 42.0, "play_duration": 300.0,
		"created_at": "2022-06-01T17:18:11Z", "urls": []string{}, "o_history": []string{"2023-02-01T21:00:00Z", "2023-01-05 20:30:00", "2023-02-01T21:00:00Z"},
		"files": []map[string]any{
			{"path": fmt.Sprintf("/data/s%d.mp4", id), "basename": fmt.Sprintf("s%d.mp4", id), "size": 100, "duration": 61.6},
			{"path": fmt.Sprintf("/data/dup/s%d.mp4", id), "basename": fmt.Sprintf("s%d.mp4", id)},
		},
		"paths": map[string]any{}, "studio": map[string]any{"id": "11"}, "performers": []map[string]any{{"id": "20"}},
		"tags": []map[string]any{{"id": "1"}, {"id": "2"}},
		"scene_markers": []map[string]any{
			{"id": fmt.Sprintf("m%d", id), "title": "", "seconds": 12.6, "end_seconds": 30.4, "primary_tag": map[string]any{"id": "2", "name": "Blowjob"}, "tags": []map[string]any{{"id": "1"}}},
		},
	}
}

// --- fake GoonHub -------------------------------------------------------------

type ghScene struct {
	goonhub.ImportSceneRequest
	ID uint
}

type ghMarker struct {
	ID      uint
	SceneID uint
	TS      int
	End     *int
	Label   string
	Tags    []uint
}

type fakeGH struct {
	mu       sync.Mutex
	v2       bool
	nextID   uint
	tags     map[uint]string
	studios  map[uint]string
	parents  map[uint]uint
	actors   map[uint]string
	scenes   map[uint]*ghScene
	markers  map[uint]*ghMarker
	writes   int
	srv      *httptest.Server
	failOnce map[string]bool
}

func newFakeGH(v2 bool) *fakeGH {
	g := &fakeGH{v2: v2, nextID: 100, tags: map[uint]string{1: "anal"}, studios: map[uint]string{}, parents: map[uint]uint{},
		actors: map[uint]string{}, scenes: map[uint]*ghScene{}, markers: map[uint]*ghMarker{}, failOnce: map[string]bool{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.handle))
	return g
}

var reID = regexp.MustCompile(`/(\d+)(/|$)`)

func pathID(p string) uint {
	m := reID.FindStringSubmatch(p)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return uint(n)
}

func (g *fakeGH) handle(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1")
	raw, _ := io.ReadAll(r.Body)
	js := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Method != http.MethodGet && p != "/auth/login" && !strings.HasSuffix(p, "/summary") {
		g.writes++
	}
	if g.failOnce[r.Method+" "+p] {
		delete(g.failOnce, r.Method+" "+p)
		js(503, map[string]string{"error": "transient"})
		return
	}
	switch {
	case p == "/auth/login":
		http.SetCookie(w, &http.Cookie{Name: "goonhub_auth", Value: "tok"})
		js(200, map[string]any{})
	case r.Header.Get("Authorization") != "Bearer tok":
		js(401, map[string]string{"error": "unauthorized"})
	case p == "/auth/me":
		js(200, map[string]any{"id": 1, "role": "admin"})
	case p == "/admin/import/capabilities":
		if !g.v2 {
			js(404, map[string]string{"error": "not found"})
			return
		}
		js(200, map[string]any{"version": 2, "features": []string{"marker_end", "jizz_history"}})
	case p == "/admin/storage-paths":
		js(200, map[string]any{"storage_paths": []map[string]any{{"id": 2, "path": "/media"}}})
	case p == "/tags" && r.Method == http.MethodGet:
		var l []map[string]any
		for id, n := range g.tags {
			l = append(l, map[string]any{"id": id, "name": n})
		}
		js(200, map[string]any{"data": l})
	case p == "/tags":
		var in struct{ Name string }
		_ = json.Unmarshal(raw, &in)
		for _, n := range g.tags {
			if n == in.Name {
				js(409, map[string]string{"error": "exists"})
				return
			}
		}
		g.nextID++
		g.tags[g.nextID] = in.Name
		js(201, map[string]any{"id": g.nextID, "name": in.Name})
	case (p == "/studios" || p == "/actors") && r.Method == http.MethodGet:
		src := g.studios
		if p == "/actors" {
			src = g.actors
		}
		var l []map[string]any
		for id, n := range src {
			l = append(l, map[string]any{"id": id, "name": n, "uuid": fmt.Sprintf("u%d", id)})
		}
		js(200, map[string]any{"data": l, "total": len(l)})
	case p == "/admin/studios" || p == "/admin/actors":
		var in struct{ Name string }
		_ = json.Unmarshal(raw, &in)
		g.nextID++
		if p == "/admin/studios" {
			g.studios[g.nextID] = in.Name
		} else {
			g.actors[g.nextID] = in.Name
		}
		js(201, map[string]any{"id": g.nextID, "uuid": fmt.Sprintf("u%d", g.nextID)})
	case strings.HasPrefix(p, "/admin/studios/") && r.Method == http.MethodPut:
		var in struct {
			ParentID *uint `json:"parent_id"`
		}
		_ = json.Unmarshal(raw, &in)
		if in.ParentID != nil {
			g.parents[pathID(p)] = *in.ParentID
		}
		js(200, map[string]any{})
	case strings.HasPrefix(p, "/actors/u") && strings.HasSuffix(p, "/interactions"):
		js(200, map[string]any{"rating": 0, "liked": false})
	case strings.HasPrefix(p, "/actors/u"):
		js(200, map[string]any{})
	case p == "/admin/import/scenes":
		var in goonhub.ImportSceneRequest
		_ = json.Unmarshal(raw, &in)
		for _, s := range g.scenes {
			if s.StoredPath == in.StoredPath {
				if !g.v2 || !in.UpdateExisting {
					js(409, map[string]any{"id": s.ID})
					return
				}
				s.ImportSceneRequest = in
				js(200, map[string]any{"id": s.ID, "created": false})
				return
			}
		}
		g.nextID++
		if !g.v2 {
			in.TagIDs, in.ActorIDs = nil, nil
		}
		g.scenes[g.nextID] = &ghScene{ImportSceneRequest: in, ID: g.nextID}
		js(201, map[string]any{"id": g.nextID, "created": true})
	case strings.HasPrefix(p, "/scenes/") && strings.HasSuffix(p, "/tags") && r.Method == http.MethodPut:
		var in struct {
			TagIDs []uint `json:"tag_ids"`
		}
		_ = json.Unmarshal(raw, &in)
		g.scenes[pathID(p)].TagIDs = in.TagIDs
		js(200, map[string]any{})
	case strings.HasPrefix(p, "/scenes/") && strings.HasSuffix(p, "/actors") && r.Method == http.MethodPut:
		var in struct {
			ActorIDs []uint `json:"actor_ids"`
		}
		_ = json.Unmarshal(raw, &in)
		g.scenes[pathID(p)].ActorIDs = in.ActorIDs
		js(200, map[string]any{})
	case strings.HasPrefix(p, "/scenes/") && r.Method == http.MethodPut:
		js(200, map[string]any{})
	case strings.HasPrefix(p, "/scenes/") && strings.HasSuffix(p, "/interactions"):
		js(200, map[string]any{"rating": 0, "liked": false, "jizzed_count": 0})
	case strings.HasPrefix(p, "/scenes/") && strings.HasSuffix(p, "/jizzed"):
		js(200, map[string]any{})
	case strings.HasPrefix(p, "/scenes/") && strings.HasSuffix(p, "/markers"):
		var l []map[string]any
		for _, m := range g.markers {
			if m.SceneID == pathID(p) {
				l = append(l, map[string]any{"id": m.ID, "timestamp": m.TS, "label": m.Label})
			}
		}
		js(200, map[string]any{"markers": l})
	case p == "/admin/import/markers":
		var in goonhub.ImportMarkerRequest
		_ = json.Unmarshal(raw, &in)
		if g.v2 {
			for _, m := range g.markers {
				if m.SceneID == in.SceneID && m.TS == in.Timestamp && m.Label == in.Label {
					m.Tags = in.TagIDs
					if in.EndTimestamp != nil {
						m.End = in.EndTimestamp
					}
					js(200, map[string]any{"id": m.ID, "created": false})
					return
				}
			}
		}
		g.nextID++
		g.markers[g.nextID] = &ghMarker{ID: g.nextID, SceneID: in.SceneID, TS: in.Timestamp, End: in.EndTimestamp, Label: in.Label, Tags: in.TagIDs}
		js(201, map[string]any{"id": g.nextID, "created": true})
	case strings.HasPrefix(p, "/markers/"):
		var in struct {
			TagIDs []uint `json:"tag_ids"`
		}
		_ = json.Unmarshal(raw, &in)
		g.markers[pathID(p)].Tags = in.TagIDs
		js(200, map[string]any{})
	case p == "/admin/search/reindex":
		js(200, map[string]any{})
	default:
		js(404, map[string]string{"error": "no route " + r.Method + " " + p})
	}
}

// --- harness --------------------------------------------------------------

func runImport(t *testing.T, stashURL string, g *fakeGH, statePath string, mutate func(*config.Config)) *Report {
	t.Helper()
	cfg := config.Default()
	cfg.Stash.URL, cfg.Stash.APIKey, cfg.Stash.PageSize = stashURL, "k", 2
	cfg.GoonHub.URL, cfg.GoonHub.Username, cfg.GoonHub.Password = g.srv.URL, "admin", "pw"
	cfg.StateFile, cfg.ReportFile = statePath, ""
	cfg.Retry = config.RetryConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	cfg.PathMappings = []config.PathMapping{{StashPrefix: "/data", GoonHubPrefix: "/media/data", StoragePathID: 2}}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	retry := httpx.Policy{MaxAttempts: cfg.Retry.MaxAttempts, InitialBackoff: cfg.Retry.InitialBackoff, MaxBackoff: cfg.Retry.MaxBackoff}
	st, err := state.Open(statePath, cfg.GoonHub.URL, cfg.DryRun)
	if err != nil {
		t.Fatal(err)
	}
	rep := NewReport(cfg.DryRun)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	imp := New(cfg, stash.New(stashURL, "k", 5*time.Second, retry), goonhub.New(g.srv.URL, "admin", "pw", 5*time.Second, retry), st, rep, log)
	ctx := context.Background()
	if err := imp.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := imp.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return rep
}

func scenes(n int) []map[string]any {
	var l []map[string]any
	for i := 1; i <= n; i++ {
		l = append(l, stashScene(i))
	}
	return l
}

func TestImportV2EndToEnd(t *testing.T) {
	sv := fakeStash(t, scenes(5))
	defer sv.Close()
	g := newFakeGH(true)
	defer g.srv.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")
	g.failOnce["POST /admin/import/scenes"] = true // transient 503 must be retried

	rep := runImport(t, sv.URL, g, statePath, nil)
	if errs, _ := rep.Counts(); errs != 0 {
		t.Fatalf("unexpected errors: %+v", rep.issues)
	}
	if len(g.scenes) != 5 || len(g.markers) != 5 || len(g.actors) != 1 || len(g.studios) != 2 {
		t.Fatalf("got scenes=%d markers=%d actors=%d studios=%d", len(g.scenes), len(g.markers), len(g.actors), len(g.studios))
	}
	if len(g.tags) != 2 {
		t.Fatalf("existing tag 'anal' must be reused case-insensitively, got %v", g.tags)
	}
	var s *ghScene
	for _, x := range g.scenes {
		if x.StoredPath == "/media/data/s1.mp4" {
			s = x
		}
	}
	if s == nil {
		t.Fatal("scene 1 not imported at the mapped path")
	}
	if *s.ReleaseDate != "2021-03-01" || s.Duration != 62 || len(s.TagIDs) != 2 || len(s.ActorIDs) != 1 || s.StudioID == nil {
		t.Fatalf("bad scene payload: %+v", s.ImportSceneRequest)
	}
	in := s.Interactions
	if in == nil || *in.Rating != 4.5 || *in.JizzedCount != 2 || len(in.Watches) != 2 || in.Watches[1].LastPosition != 42 || *s.ViewCount != 3 {
		t.Fatalf("bad interactions: %+v", in)
	}
	// o history: deduplicated, normalized to RFC 3339 UTC and sorted
	if len(in.JizzHistory) != 2 || in.JizzHistory[0] != "2023-01-05T20:30:00Z" || in.JizzHistory[1] != "2023-02-01T21:00:00Z" {
		t.Fatalf("bad jizz history: %v", in.JizzHistory)
	}
	for _, m := range g.markers {
		if m.Label != "Blowjob" || m.TS != 13 || m.End == nil || *m.End != 30 || len(m.Tags) != 2 {
			t.Fatalf("bad marker: %+v", m)
		}
	}
	if len(g.parents) != 1 {
		t.Fatalf("studio parent not set: %v", g.parents)
	}

	// Re-run: nothing must be written.
	before := g.writes
	rep = runImport(t, sv.URL, g, statePath, func(c *config.Config) { c.PostImport.Reindex = false })
	if g.writes != before {
		t.Fatalf("re-run wrote %d times", g.writes-before)
	}
	if got := rep.S("scenes").Unchanged.Load(); got != 5 {
		t.Fatalf("re-run: unchanged scenes = %d", got)
	}

	// Lost state: no duplicate anywhere.
	rep = runImport(t, sv.URL, g, filepath.Join(t.TempDir(), "fresh.json"), nil)
	if len(g.scenes) != 5 || len(g.markers) != 5 || len(g.actors) != 1 || len(g.studios) != 2 || len(g.tags) != 2 {
		t.Fatalf("duplicates after lost state: scenes=%d markers=%d actors=%d studios=%d tags=%d", len(g.scenes), len(g.markers), len(g.actors), len(g.studios), len(g.tags))
	}
	if errs, _ := rep.Counts(); errs != 0 {
		t.Fatalf("unexpected errors after lost state: %+v", rep.issues)
	}
}

func TestImportV1EndToEnd(t *testing.T) {
	sv := fakeStash(t, scenes(3))
	defer sv.Close()
	g := newFakeGH(false)
	defer g.srv.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")

	rep := runImport(t, sv.URL, g, statePath, nil)
	if errs, _ := rep.Counts(); errs != 0 {
		t.Fatalf("unexpected errors: %+v", rep.issues)
	}
	for _, s := range g.scenes {
		if len(s.TagIDs) != 2 || len(s.ActorIDs) != 1 {
			t.Fatalf("v1 associations not applied: %+v", s.ImportSceneRequest)
		}
	}
	for _, m := range g.markers {
		if m.End != nil {
			t.Fatalf("v1 server must not receive a marker end: %+v", m)
		}
	}
	// Lost state: markers are deduplicated client-side.
	runImport(t, sv.URL, g, filepath.Join(t.TempDir(), "fresh.json"), nil)
	if len(g.scenes) != 3 || len(g.markers) != 3 {
		t.Fatalf("duplicates after lost state in v1: scenes=%d markers=%d", len(g.scenes), len(g.markers))
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	sv := fakeStash(t, scenes(3))
	defer sv.Close()
	g := newFakeGH(true)
	defer g.srv.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")
	rep := runImport(t, sv.URL, g, statePath, func(c *config.Config) { c.DryRun = true })
	if g.writes != 0 {
		t.Fatalf("dry-run wrote %d times", g.writes)
	}
	if rep.S("scenes").Created.Load() != 3 || rep.S("markers").Created.Load() != 3 {
		t.Fatalf("dry-run predictions wrong: %v %v", rep.S("scenes").snapshot(), rep.S("markers").snapshot())
	}
}
