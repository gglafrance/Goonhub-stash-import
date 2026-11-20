package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	reYear  = regexp.MustCompile(`^\d{4}$`)
	reMonth = regexp.MustCompile(`^\d{4}-\d{2}$`)
	reDay   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// normalizeDate converts Stash dates (YYYY, YYYY-MM, YYYY-MM-DD, possibly with a
// time part) to the strict YYYY-MM-DD GoonHub expects. ok=false means the value
// is present but unusable.
func normalizeDate(s *string) (out *string, ok bool) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, true
	}
	v := strings.TrimSpace(*s)
	if len(v) > 10 && (v[10] == 'T' || v[10] == ' ') {
		v = v[:10]
	}
	switch {
	case reDay.MatchString(v):
	case reMonth.MatchString(v):
		v += "-01"
	case reYear.MatchString(v):
		v += "-01-01"
	default:
		return nil, false
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return nil, false
	}
	return &v, true
}

// yearOf returns the year of a Stash date, or nil.
func yearOf(s *string) *int {
	d, ok := normalizeDate(s)
	if !ok || d == nil {
		return nil
	}
	y, err := strconv.Atoi((*d)[:4])
	if err != nil {
		return nil
	}
	return &y
}

// ratingStars converts Stash rating100 (1..100) to GoonHub stars (0.5..5, step 0.5).
func ratingStars(r *int) *float64 {
	if r == nil || *r <= 0 {
		return nil
	}
	v := math.Round(float64(*r)/20.0*2) / 2
	v = math.Max(0.5, math.Min(5, v))
	return &v
}

var defaultGenders = map[string]string{
	"MALE":               "male",
	"FEMALE":             "female",
	"TRANSGENDER_MALE":   "transgender_male",
	"TRANSGENDER_FEMALE": "transgender_female",
	"INTERSEX":           "intersex",
	"NON_BINARY":         "non_binary",
}

func mapGender(g *string, overrides map[string]string) string {
	if g == nil || *g == "" {
		return ""
	}
	k := strings.ToUpper(*g)
	for ok, v := range overrides {
		if strings.ToUpper(ok) == k {
			return v
		}
	}
	if v, ok := defaultGenders[k]; ok {
		return v
	}
	return strings.ToLower(k)
}

func isFakeBoobs(v *string) bool {
	if v == nil {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(*v))
	return s != "" && s != "natural" && s != "no" && s != "n"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// truncateRunes cuts s to n runes; truncated reports whether something was cut.
func truncateRunes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

func titleFor(title *string, basename, fallback string) string {
	if t := deref(title); t != "" {
		return t
	}
	if fallback == "basename_no_ext" {
		if ext := path.Ext(basename); ext != "" && len(ext) < len(basename) {
			return strings.TrimSuffix(basename, ext)
		}
	}
	return basename
}

// markerLabel mirrors what Stash displays: the title, or the primary tag name.
func markerLabel(title string, primaryTag string) string {
	l := strings.TrimSpace(title)
	if l == "" {
		l = strings.TrimSpace(primaryTag)
	}
	l, _ = truncateRunes(l, 100)
	return l
}

// rfc3339 normalizes a Stash timestamp; nil when unparsable.
func rfc3339(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, *s); err == nil {
			v := t.UTC().Format(time.RFC3339)
			return &v
		}
	}
	return nil
}

func hashOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("hashOf: %v", err)) // only called with plain structs
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

func normName(name, strategy string) string {
	if strategy == "name" {
		return strings.TrimSpace(name)
	}
	return strings.ToLower(strings.TrimSpace(name))
}

func ptr[T any](v T) *T { return &v }
