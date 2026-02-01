package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// IDMap tracks Stash ID -> GoonHub ID mappings for idempotent imports.
type IDMap struct {
	Tags    map[string]uint `json:"tags"`
	Studios map[string]uint `json:"studios"`
	Actors  map[string]uint `json:"actors"`
	Scenes  map[string]uint `json:"scenes"`
	Markers map[string]uint `json:"markers"`
}

func NewIDMap() *IDMap {
	return &IDMap{
		Tags:    make(map[string]uint),
		Studios: make(map[string]uint),
		Actors:  make(map[string]uint),
		Scenes:  make(map[string]uint),
		Markers: make(map[string]uint),
	}
}

// Load reads the ID map from a JSON file. Returns a new empty map if the file doesn't exist.
func LoadIDMap(path string) (*IDMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewIDMap(), nil
		}
		return nil, fmt.Errorf("failed to read id map: %w", err)
	}

	idMap := NewIDMap()
	if err := json.Unmarshal(data, idMap); err != nil {
		return nil, fmt.Errorf("failed to parse id map: %w", err)
	}

	// Ensure all maps are initialized even if absent in JSON
	if idMap.Tags == nil {
		idMap.Tags = make(map[string]uint)
	}
	if idMap.Studios == nil {
		idMap.Studios = make(map[string]uint)
	}
	if idMap.Actors == nil {
		idMap.Actors = make(map[string]uint)
	}
	if idMap.Scenes == nil {
		idMap.Scenes = make(map[string]uint)
	}
	if idMap.Markers == nil {
		idMap.Markers = make(map[string]uint)
	}

	return idMap, nil
}

// Save writes the ID map to a JSON file.
func (m *IDMap) Save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal id map: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write id map: %w", err)
	}
	return nil
}
