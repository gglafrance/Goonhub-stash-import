package stash

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const tagFields = `id name description aliases parents { id name }`
const studioFields = `id name urls details rating100 favorite aliases image_path parent_studio { id }`
const performerFields = `id name disambiguation alias_list gender birthdate death_date ethnicity country
	eye_color hair_color height_cm weight measurements fake_tits tattoos piercings career_start career_end
	favorite rating100 image_path`
const sceneFields = `id title code details director urls date rating100 organized o_counter play_count
	play_history o_history resume_time play_duration created_at
	files { path basename size duration width height video_codec audio_codec frame_rate bit_rate mod_time }
	paths { screenshot }
	studio { id }
	performers { id }
	tags { id }
	scene_markers { id title seconds end_seconds primary_tag { id name } tags { id } }`

// fetchAll pages through a find* query until every item is retrieved.
func fetchAll[T any](ctx context.Context, c *Client, root, listField, fields string, perPage int) ([]T, error) {
	q := fmt.Sprintf(`query($filter: FindFilterType) { %s(filter: $filter) { count %s { %s } } }`, root, listField, fields)
	var all []T
	for page := 1; ; page++ {
		// Shape: {root: {count: N, listField: [...]}}
		var raw map[string]map[string]json.RawMessage
		if err := c.Query(ctx, q, map[string]any{"filter": map[string]any{"page": page, "per_page": perPage, "sort": "id", "direction": "ASC"}}, &raw); err != nil {
			return nil, fmt.Errorf("%s page %d: %w", root, page, err)
		}
		var count int
		if err := json.Unmarshal(raw[root]["count"], &count); err != nil {
			return nil, fmt.Errorf("%s: bad count: %w", root, err)
		}
		var items []T
		if err := json.Unmarshal(raw[root][listField], &items); err != nil {
			return nil, fmt.Errorf("%s: bad items: %w", root, err)
		}
		all = append(all, items...)
		if len(items) == 0 || len(all) >= count {
			return all, nil
		}
	}
}

func (c *Client) Tags(ctx context.Context, perPage int) ([]Tag, error) {
	return fetchAll[Tag](ctx, c, "findTags", "tags", tagFields, perPage)
}

func (c *Client) Studios(ctx context.Context, perPage int) ([]Studio, error) {
	return fetchAll[Studio](ctx, c, "findStudios", "studios", studioFields, perPage)
}

func (c *Client) Performers(ctx context.Context, perPage int) ([]Performer, error) {
	return fetchAll[Performer](ctx, c, "findPerformers", "performers", performerFields, perPage)
}

// SceneQuery selects which scenes to read.
type SceneQuery struct {
	Filter  map[string]any // SceneFilterType, may be nil
	IDs     []string       // restrict to these IDs, may be nil
	Sort    string
	PerPage int
	Fields  string // GraphQL selection for scenes; "" = everything the importer needs
}

// ScenesPage returns one page of scenes plus the total matching count.
func (c *Client) ScenesPage(ctx context.Context, sq SceneQuery, page int) ([]Scene, int, error) {
	args := []string{"filter: $filter"}
	decl := []string{"$filter: FindFilterType"}
	vars := map[string]any{"filter": map[string]any{"page": page, "per_page": sq.PerPage, "sort": sq.Sort, "direction": "ASC"}}
	if len(sq.Filter) > 0 {
		decl = append(decl, "$scene_filter: SceneFilterType")
		args = append(args, "scene_filter: $scene_filter")
		vars["scene_filter"] = sq.Filter
	}
	if len(sq.IDs) > 0 {
		decl = append(decl, "$ids: [ID!]")
		args = append(args, "ids: $ids")
		vars["ids"] = sq.IDs
	}
	fields := sq.Fields
	if fields == "" {
		fields = sceneFields
	}
	q := fmt.Sprintf(`query(%s) { findScenes(%s) { count scenes { %s } } }`,
		strings.Join(decl, ", "), strings.Join(args, ", "), fields)
	var out struct {
		FindScenes struct {
			Count  int     `json:"count"`
			Scenes []Scene `json:"scenes"`
		} `json:"findScenes"`
	}
	if err := c.Query(ctx, q, vars, &out); err != nil {
		return nil, 0, fmt.Errorf("findScenes page %d: %w", page, err)
	}
	return out.FindScenes.Scenes, out.FindScenes.Count, nil
}

// Version returns the Stash version string (connectivity check).
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	if err := c.Query(ctx, `{ version { version } }`, nil, &out); err != nil {
		return "", err
	}
	return out.Version.Version, nil
}
