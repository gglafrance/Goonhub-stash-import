package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExpandsEnvAndValidates(t *testing.T) {
	t.Setenv("T_KEY", "secret$withdollar")
	p := filepath.Join(t.TempDir(), "c.yaml")
	yml := `
stash:
  url: ${T_URL:-http://stash:9999}
  api_key: ${T_KEY}
goonhub:
  url: http://gh
  username: admin
  password: pa$$word
concurrency: 8
path_mappings:
  - {stash_prefix: /data, goonhub_prefix: /media/data, storage_path_id: 2}
`
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Stash.URL != "http://stash:9999" || c.Stash.APIKey != "secret$withdollar" || c.GoonHub.Password != "pa$$word" {
		t.Fatalf("bad expansion: %+v %+v", c.Stash, c.GoonHub)
	}
	if c.Concurrency != 8 || c.Stash.PageSize != 250 || !c.Phases.Markers {
		t.Fatal("defaults must be kept for keys absent from the file")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	_ = os.WriteFile(p, []byte("concurency: 3\n"), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "concurency") {
		t.Fatalf("a typo in a key must be reported, got %v", err)
	}
}

func TestValidateCollectsErrors(t *testing.T) {
	c := Default()
	c.OnExisting = "replace"
	err := c.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"stash.api_key", "on_existing", "path_mappings"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}
