package importer

import "testing"

func TestNormalizeDate(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2020-05-17", "2020-05-17", true},
		{"2020-05", "2020-05-01", true},
		{"2020", "2020-01-01", true},
		{"2020-05-17T10:00:00Z", "2020-05-17", true},
		{"2020-13-01", "", false},
		{"17/05/2020", "", false},
		{"", "", true},
	}
	for _, tt := range tests {
		in := tt.in
		got, ok := normalizeDate(&in)
		if ok != tt.ok {
			t.Fatalf("normalizeDate(%q) ok=%v, want %v", tt.in, ok, tt.ok)
		}
		g := ""
		if got != nil {
			g = *got
		}
		if g != tt.want {
			t.Fatalf("normalizeDate(%q) = %q, want %q", tt.in, g, tt.want)
		}
	}
}

func TestRatingStars(t *testing.T) {
	tests := []struct {
		in   int
		want float64
	}{{100, 5}, {80, 4}, {90, 4.5}, {1, 0.5}, {55, 3}, {50, 2.5}}
	for _, tt := range tests {
		in := tt.in
		got := ratingStars(&in)
		if got == nil || *got != tt.want {
			t.Fatalf("ratingStars(%d) = %v, want %v", tt.in, got, tt.want)
		}
	}
	zero := 0
	if ratingStars(&zero) != nil || ratingStars(nil) != nil {
		t.Fatal("ratingStars must return nil for 0 / nil")
	}
}

func TestTitleAndLabel(t *testing.T) {
	if got := titleFor(nil, "My Video.mp4", "basename_no_ext"); got != "My Video" {
		t.Fatalf("titleFor = %q", got)
	}
	if got := titleFor(nil, "My Video.mp4", "basename"); got != "My Video.mp4" {
		t.Fatalf("titleFor = %q", got)
	}
	ttl := "  Real title "
	if got := titleFor(&ttl, "x.mp4", "basename_no_ext"); got != "Real title" {
		t.Fatalf("titleFor = %q", got)
	}
	if got := markerLabel("", "Blowjob"); got != "Blowjob" {
		t.Fatalf("markerLabel fallback = %q", got)
	}
	long := ""
	for i := 0; i < 150; i++ {
		long += "é"
	}
	if got := markerLabel(long, ""); len([]rune(got)) != 100 {
		t.Fatalf("markerLabel must truncate to 100 runes, got %d", len([]rune(got)))
	}
}

func TestMapGender(t *testing.T) {
	g := "TRANSGENDER_FEMALE"
	if got := mapGender(&g, nil); got != "transgender_female" {
		t.Fatalf("mapGender = %q", got)
	}
	if got := mapGender(&g, map[string]string{"transgender_female": "trans"}); got != "trans" {
		t.Fatalf("mapGender override = %q", got)
	}
}
