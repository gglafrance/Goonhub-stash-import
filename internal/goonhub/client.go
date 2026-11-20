// Package goonhub is the GoonHub REST client used by the importer.
package goonhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"goonhub-stash-importer/internal/httpx"
)

type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	retry    httpx.Policy

	mu    sync.RWMutex
	token string
}

func New(baseURL, username, password string, timeout time.Duration, retry httpx.Policy) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: timeout},
		retry:    retry,
	}
}

// ConflictError is a 409 carrying the ID of the existing entity when provided.
type ConflictError struct {
	ExistingID uint
	Body       string
}

func (e *ConflictError) Error() string { return "conflict: " + e.Body }

func (e *ConflictError) HTTPStatus() int { return http.StatusConflict }

func IsConflict(err error) (*ConflictError, bool) {
	var ce *ConflictError
	ok := errors.As(err, &ce)
	return ce, ok
}

func IsNotFound(err error) bool { return httpx.StatusOf(err) == http.StatusNotFound }

// Login authenticates and stores the token from the goonhub_auth cookie.
func (c *Client) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.username, "password": c.password})
	return c.retry.Do(ctx, true, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/auth/login", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("login request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return &httpx.StatusError{Method: "POST", URL: "/api/v1/auth/login", Status: resp.StatusCode, Body: string(b)}
		}
		for _, ck := range resp.Cookies() {
			if ck.Name == "goonhub_auth" {
				c.mu.Lock()
				c.token = ck.Value
				c.mu.Unlock()
				return nil
			}
		}
		return errors.New("login succeeded but no goonhub_auth cookie was returned")
	})
}

// do sends a request; contentType "" means JSON body. A 401 triggers one re-login.
func (c *Client) do(ctx context.Context, method, path string, body []byte, contentType string, out any, idempotent bool) error {
	relogged := false
	return c.retry.Do(ctx, idempotent, func() error {
		for {
			err := c.once(ctx, method, path, body, contentType, out)
			if httpx.StatusOf(err) == http.StatusUnauthorized && !relogged {
				relogged = true
				if lerr := c.Login(ctx); lerr != nil {
					return fmt.Errorf("re-login after 401 failed: %w", lerr)
				}
				continue
			}
			return err
		}
	})
}

func (c *Client) once(ctx context.Context, method, path string, body []byte, contentType string, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	c.mu.RLock()
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.mu.RUnlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: failed to read response: %w", method, path, err)
	}
	if resp.StatusCode == http.StatusConflict {
		var cd struct {
			ID uint `json:"id"`
		}
		_ = json.Unmarshal(raw, &cd)
		return &ConflictError{ExistingID: cd.ID, Body: string(raw)}
	}
	if resp.StatusCode >= 300 {
		return &httpx.StatusError{Method: method, URL: path, Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: failed to decode response: %w", method, path, err)
		}
	}
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any, idempotent bool) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
	}
	return c.do(ctx, method, path, body, "", out, idempotent)
}

func (c *Client) upload(ctx context.Context, path, field, filename string, data []byte, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, buf.Bytes(), w.FormDataContentType(), out, true)
}

// --- Discovery ---

func (c *Client) Me(ctx context.Context) (uint, string, error) {
	var out struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/auth/me", nil, &out, true); err != nil {
		return 0, "", err
	}
	return out.ID, out.Role, nil
}

// Capabilities returns the import API capabilities; version 1 when the endpoint doesn't exist.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/admin/import/capabilities", nil, &out, true)
	if IsNotFound(err) {
		return Capabilities{Version: 1}, nil
	}
	return out, err
}

func (c *Client) StoragePaths(ctx context.Context) ([]StoragePath, error) {
	var out struct {
		StoragePaths []StoragePath `json:"storage_paths"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/admin/storage-paths", nil, &out, true)
	return out.StoragePaths, err
}

// --- Tags / studios / actors ---

func (c *Client) ListTags(ctx context.Context) ([]Tag, error) {
	var out struct {
		Data []Tag `json:"data"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/tags", nil, &out, true)
	return out.Data, err
}

func (c *Client) CreateTag(ctx context.Context, name, color string) (*Tag, error) {
	var out Tag
	in := map[string]string{"name": name}
	if color != "" {
		in["color"] = color
	}
	// Idempotent: a duplicate yields a 409 that the caller resolves by name.
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/tags", in, &out, true)
	return &out, err
}

// listPaged walks a {data,total} endpoint. The server clamps limit (100 by default).
func (c *Client) listPaged(ctx context.Context, path string) ([]NamedItem, error) {
	var all []NamedItem
	for page := 1; ; page++ {
		var out struct {
			Data  []NamedItem `json:"data"`
			Total int         `json:"total"`
		}
		p := fmt.Sprintf("%s?page=%d&limit=100", path, page) // default order: name ASC
		if err := c.doJSON(ctx, http.MethodGet, p, nil, &out, true); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if len(out.Data) == 0 || len(all) >= out.Total {
			return all, nil
		}
	}
}

func (c *Client) ListStudios(ctx context.Context) ([]NamedItem, error) {
	return c.listPaged(ctx, "/api/v1/studios")
}

func (c *Client) ListActors(ctx context.Context) ([]NamedItem, error) {
	return c.listPaged(ctx, "/api/v1/actors")
}

func (c *Client) CreateStudio(ctx context.Context, r StudioRequest) (NamedItem, error) {
	var out NamedItem
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/admin/studios", r, &out, false)
	return out, err
}

// UpdateStudio sends only the given fields (nil fields untouched server-side).
func (c *Client) UpdateStudio(ctx context.Context, id uint, fields map[string]any) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/admin/studios/%d", id), fields, nil, true)
}

func (c *Client) CreateActor(ctx context.Context, r ActorRequest) (NamedItem, error) {
	var out NamedItem
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/admin/actors", r, &out, false)
	return out, err
}

// ActorInteractions returns the current user's rating/like for an actor.
func (c *Client) ActorInteractions(ctx context.Context, uuid string) (float64, bool, error) {
	var out struct {
		Rating float64 `json:"rating"`
		Liked  bool    `json:"liked"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/actors/"+uuid+"/interactions", nil, &out, true)
	return out.Rating, out.Liked, err
}

func (c *Client) SetActorRating(ctx context.Context, uuid string, rating float64) error {
	return c.doJSON(ctx, http.MethodPut, "/api/v1/actors/"+uuid+"/rating", map[string]float64{"rating": rating}, nil, true)
}

// ToggleActorLike flips the like state (not idempotent).
func (c *Client) ToggleActorLike(ctx context.Context, uuid string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/actors/"+uuid+"/like", nil, nil, false)
}

func (c *Client) UpdateActor(ctx context.Context, id uint, r ActorRequest) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/admin/actors/%d", id), r, nil, true)
}

func (c *Client) UploadActorImage(ctx context.Context, id uint, filename string, data []byte) error {
	return c.upload(ctx, fmt.Sprintf("/api/v1/admin/actors/%d/image", id), "image", filename, data, nil)
}

// --- Scenes ---

// ImportScene returns the scene ID and whether it was created. A 409 is returned
// as *ConflictError (v1, or v2 without update_existing).
func (c *Client) ImportScene(ctx context.Context, r ImportSceneRequest) (uint, bool, error) {
	var out struct {
		ID      uint  `json:"id"`
		Created *bool `json:"created"`
	}
	// Idempotent thanks to stored_path dedup (409 on replay).
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/admin/import/scenes", r, &out, true); err != nil {
		return 0, false, err
	}
	created := out.Created == nil || *out.Created
	return out.ID, created, nil
}

func (c *Client) SetSceneTags(ctx context.Context, id uint, tagIDs []uint) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/scenes/%d/tags", id), map[string]any{"tag_ids": nonNil(tagIDs)}, nil, true)
}

func (c *Client) SetSceneActors(ctx context.Context, id uint, actorIDs []uint) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/scenes/%d/actors", id), map[string]any{"actor_ids": nonNil(actorIDs)}, nil, true)
}

func (c *Client) SetSceneStudio(ctx context.Context, id uint, studioID *uint) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/scenes/%d/studio", id), map[string]any{"studio_id": studioID}, nil, true)
}

func (c *Client) UpdateSceneDetails(ctx context.Context, id uint, title, description string, releaseDate *string) error {
	in := map[string]any{"title": title, "description": description}
	if releaseDate != nil {
		in["release_date"] = *releaseDate
	}
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/scenes/%d/details", id), in, nil, true)
}

func (c *Client) UploadThumbnail(ctx context.Context, id uint, filename string, data []byte) error {
	return c.upload(ctx, fmt.Sprintf("/api/v1/scenes/%d/thumbnail/upload", id), "thumbnail", filename, data, nil)
}

// GetScene returns the raw scene (v1 verification).
func (c *Client) GetScene(ctx context.Context, id uint) (*SceneSummary, error) {
	var out struct {
		ID         uint    `json:"id"`
		Title      string  `json:"title"`
		StoredPath string  `json:"stored_path"`
		StudioID   *uint   `json:"studio_id"`
		ViewCount  int64   `json:"view_count"`
		TrashedAt  *string `json:"trashed_at"`
	}
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/scenes/%d", id), nil, &out, true); err != nil {
		return nil, err
	}
	return &SceneSummary{ID: out.ID, Title: out.Title, StoredPath: out.StoredPath, StudioID: out.StudioID,
		ViewCount: out.ViewCount, Trashed: out.TrashedAt != nil}, nil
}

// SceneTagIDs / SceneActorIDs read the association tables (the denormalized
// scenes.tags field returned by GET /scenes/:id is not kept in sync by GoonHub).
func (c *Client) SceneTagIDs(ctx context.Context, id uint) ([]uint, error) {
	return c.assocIDs(ctx, fmt.Sprintf("/api/v1/scenes/%d/tags", id))
}

func (c *Client) SceneActorIDs(ctx context.Context, id uint) ([]uint, error) {
	return c.assocIDs(ctx, fmt.Sprintf("/api/v1/scenes/%d/actors", id))
}

func (c *Client) assocIDs(ctx context.Context, path string) ([]uint, error) {
	var out struct {
		Data []struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out, true); err != nil {
		return nil, err
	}
	ids := make([]uint, len(out.Data))
	for i, x := range out.Data {
		ids[i] = x.ID
	}
	return ids, nil
}

// SceneSummaries uses the v2 batch endpoint (max 1000 IDs).
func (c *Client) SceneSummaries(ctx context.Context, ids []uint) ([]SceneSummary, []uint, error) {
	var out struct {
		Data    []SceneSummary `json:"data"`
		Missing []uint         `json:"missing"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/admin/import/scenes/summary", map[string]any{"ids": ids}, &out, true)
	return out.Data, out.Missing, err
}

// --- Interactions (v1 fallback, current user) ---

func (c *Client) GetInteractions(ctx context.Context, id uint) (rating float64, liked bool, jizzed int, err error) {
	var out struct {
		Rating      float64 `json:"rating"`
		Liked       bool    `json:"liked"`
		JizzedCount int     `json:"jizzed_count"`
	}
	err = c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/scenes/%d/interactions", id), nil, &out, true)
	return out.Rating, out.Liked, out.JizzedCount, err
}

func (c *Client) SetRating(ctx context.Context, id uint, rating float64) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/scenes/%d/rating", id), map[string]float64{"rating": rating}, nil, true)
}

func (c *Client) IncrementJizzed(ctx context.Context, id uint) error {
	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/scenes/%d/jizzed", id), nil, nil, false)
}

// --- Markers ---

func (c *Client) ImportMarker(ctx context.Context, r ImportMarkerRequest, idempotent bool) (uint, bool, error) {
	var out struct {
		ID      uint  `json:"id"`
		Created *bool `json:"created"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/admin/import/markers", r, &out, idempotent); err != nil {
		return 0, false, err
	}
	return out.ID, out.Created == nil || *out.Created, nil
}

// SceneMarkers lists the current user's markers on a scene.
func (c *Client) SceneMarkers(ctx context.Context, sceneID uint) ([]Marker, error) {
	var out struct {
		Markers []Marker `json:"markers"`
	}
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/scenes/%d/markers", sceneID), nil, &out, true)
	return out.Markers, err
}

func (c *Client) AddMarkerTags(ctx context.Context, markerID uint, tagIDs []uint) error {
	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/markers/%d/tags", markerID), map[string]any{"tag_ids": tagIDs}, nil, true)
}

// --- Post-import ---

func (c *Client) Reindex(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/admin/search/reindex", nil, nil, true)
}

func (c *Client) BulkPhase(ctx context.Context, phase, mode string, sceneIDs []uint) (submitted, skipped int, err error) {
	var out struct {
		Submitted int `json:"submitted"`
		Skipped   int `json:"skipped"`
	}
	in := map[string]any{"phase": phase, "mode": mode}
	if len(sceneIDs) > 0 {
		in["scene_ids"] = sceneIDs
	}
	err = c.doJSON(ctx, http.MethodPost, "/api/v1/admin/jobs/bulk", in, &out, true)
	return out.Submitted, out.Skipped, err
}

func nonNil(ids []uint) []uint {
	if ids == nil {
		return []uint{}
	}
	return ids
}
