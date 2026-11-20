// Package pathmap translates Stash file paths to GoonHub file paths.
package pathmap

import (
	"fmt"
	"sort"
	"strings"

	"goonhub-stash-importer/internal/config"
)

type Mapper struct {
	mappings []config.PathMapping // sorted by descending normalized prefix length
}

type Mapped struct {
	Path          string
	StoragePathID uint
	Mapping       config.PathMapping
}

func norm(p string) string { return strings.TrimRight(p, "/") }

// New sorts mappings so the most specific prefix wins.
func New(mappings []config.PathMapping) *Mapper {
	ms := append([]config.PathMapping(nil), mappings...)
	sort.SliceStable(ms, func(i, j int) bool { return len(norm(ms[i].StashPrefix)) > len(norm(ms[j].StashPrefix)) })
	return &Mapper{mappings: ms}
}

// Map returns the GoonHub path for stashPath. A prefix only matches on a folder
// boundary: "/data" matches "/data/x.mp4" and "/data" but not "/data2/x.mp4".
func (m *Mapper) Map(stashPath string) (*Mapped, error) {
	for _, mp := range m.mappings {
		prefix := norm(mp.StashPrefix)
		var rest string
		switch {
		case prefix == "": // root mapping "/"
			rest = strings.TrimLeft(stashPath, "/")
		case stashPath == prefix:
			rest = ""
		case strings.HasPrefix(stashPath, prefix+"/"):
			rest = stashPath[len(prefix)+1:]
		default:
			continue
		}
		dst := norm(mp.GoonHubPrefix)
		if rest != "" {
			dst += "/" + strings.TrimLeft(rest, "/")
		}
		return &Mapped{Path: dst, StoragePathID: mp.StoragePathID, Mapping: mp}, nil
	}
	return nil, fmt.Errorf("no path mapping matches %q", stashPath)
}
