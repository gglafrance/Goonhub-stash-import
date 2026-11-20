package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveReload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	s, err := Open(p, "http://gh", false)
	if err != nil {
		t.Fatal(err)
	}
	s.Put(KindScenes, "12", Entry{GH: 7, Status: StatusOK, Hash: "abc"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p, "http://gh", false)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s2.Get(KindScenes, "12")
	if !ok || e.GH != 7 || e.Hash != "abc" {
		t.Fatalf("unexpected entry after reload: %+v %v", e, ok)
	}
	if _, err := Open(p, "http://other", false); err == nil {
		t.Fatal("expected an error when the state belongs to another GoonHub")
	}
}

func TestLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"tags":{"1":10},"studios":{},"actors":{"3":30},"scenes":{"5":50},"markers":{"9":90}}`
	if err := os.WriteFile(filepath.Join(dir, "id_map.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "import_state.json"), "http://gh", false)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := s.GHID(KindPerformers, "3"); !ok || id != 30 {
		t.Fatalf("legacy actors not migrated: %d %v", id, ok)
	}
	if e, _ := s.Get(KindScenes, "5"); e.Status != StatusPartial {
		t.Fatalf("legacy scenes must be partial, got %q", e.Status)
	}
}

func TestDryRunNeverWrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(p, "http://gh", true)
	s.Put(KindTags, "1", Entry{GH: 1, Status: StatusOK})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("dry-run store must not write the state file")
	}
}
