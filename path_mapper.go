package main

import (
	"fmt"
	"strings"
)

type PathMapper struct {
	mappings []PathMapping
}

func NewPathMapper(mappings []PathMapping) *PathMapper {
	return &PathMapper{mappings: mappings}
}

type MappedPath struct {
	GoonHubPath   string
	StoragePathID uint
}

// MapPath translates a Stash file path to a GoonHub path using configured prefix mappings.
// Returns the mapped path and storage path ID, or an error if no mapping matches.
func (pm *PathMapper) MapPath(stashPath string) (*MappedPath, error) {
	for _, m := range pm.mappings {
		if strings.HasPrefix(stashPath, m.StashPrefix) {
			remainder := stashPath[len(m.StashPrefix):]
			ghPath := strings.TrimRight(m.GoonHubPrefix, "/") + "/" + strings.TrimLeft(remainder, "/")
			return &MappedPath{
				GoonHubPath:   ghPath,
				StoragePathID: m.StoragePathID,
			}, nil
		}
	}
	return nil, fmt.Errorf("no path mapping found for: %s", stashPath)
}
