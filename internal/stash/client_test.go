package stash

import "testing"

func TestEnsureReadOnly(t *testing.T) {
	ok := []string{
		`{ version { version } }`,
		`query($filter: FindFilterType) { findScenes(filter: $filter) { count } }`,
		"# comment\n{ findTags { count } }",
	}
	for _, q := range ok {
		if err := ensureReadOnly(q); err != nil {
			t.Fatalf("expected %q to be accepted: %v", q, err)
		}
	}
	bad := []string{
		`mutation { sceneUpdate(input: {id: 1}) { id } }`,
		`  MUTATION X { metadataScan(input: {}) }`,
		`subscription { jobsSubscribe { type } }`,
		`query { a } mutation { sceneDestroy(input: {id: 1}) }`,
		`{ a } mutation m { tagCreate(input: {name: "x"}) { id } }`,
	}
	for _, q := range bad {
		if err := ensureReadOnly(q); err == nil {
			t.Fatalf("expected %q to be refused", q)
		}
	}
}
