package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type GoonHubClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewGoonHubClient(baseURL string) *GoonHubClient {
	return &GoonHubClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *GoonHubClient) doRequest(method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusConflict {
		var conflictData struct {
			ID    uint   `json:"id"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal(respBody, &conflictData)
		return &ConflictError{Message: string(respBody), ExistingID: conflictData.ID}
	}

	if resp.StatusCode >= 500 {
		return &ServerError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// doWithRetry executes a request with a single retry on 5xx errors.
func (c *GoonHubClient) doWithRetry(method, path string, body any, result any) error {
	err := c.doRequest(method, path, body, result)
	if err != nil {
		if _, ok := err.(*ServerError); ok {
			time.Sleep(2 * time.Second)
			return c.doRequest(method, path, body, result)
		}
		return err
	}
	return nil
}

// --- Auth ---

func (c *GoonHubClient) Login(username, password string) error {
	// Login uses a custom flow because the token is returned via Set-Cookie header,
	// not in the response body.
	jsonBody, err := json.Marshal(GHLoginRequest{Username: username, Password: password})
	if err != nil {
		return fmt.Errorf("failed to marshal login request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/auth/login", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Extract token from Set-Cookie header (cookie name: goonhub_auth)
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "goonhub_auth" {
			c.token = cookie.Value
			return nil
		}
	}

	return fmt.Errorf("login succeeded but no auth cookie received")
}

// --- Tags ---

func (c *GoonHubClient) ListTags() ([]GHTagWithCount, error) {
	var resp struct {
		Data []GHTagWithCount `json:"data"`
	}
	if err := c.doWithRetry("GET", "/api/v1/tags", nil, &resp); err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}
	return resp.Data, nil
}

func (c *GoonHubClient) CreateTag(name, color string) (*GHTag, error) {
	req := GHCreateTagRequest{Name: name}
	if color != "" {
		req.Color = color
	}
	var tag GHTag
	if err := c.doWithRetry("POST", "/api/v1/tags", req, &tag); err != nil {
		return nil, fmt.Errorf("failed to create tag: %w", err)
	}
	return &tag, nil
}

// --- Studios ---

func (c *GoonHubClient) ListStudios() ([]GHStudioListItem, error) {
	var resp struct {
		Data []GHStudioListItem `json:"data"`
	}
	if err := c.doWithRetry("GET", "/api/v1/studios?limit=10000", nil, &resp); err != nil {
		return nil, fmt.Errorf("failed to list studios: %w", err)
	}
	return resp.Data, nil
}

func (c *GoonHubClient) CreateStudio(req GHCreateStudioRequest) (*GHStudio, error) {
	var studio GHStudio
	if err := c.doWithRetry("POST", "/api/v1/admin/studios", req, &studio); err != nil {
		return nil, fmt.Errorf("failed to create studio: %w", err)
	}
	return &studio, nil
}

func (c *GoonHubClient) UpdateStudio(id uint, req GHUpdateStudioRequest) error {
	path := fmt.Sprintf("/api/v1/admin/studios/%d", id)
	if err := c.doWithRetry("PUT", path, req, nil); err != nil {
		return fmt.Errorf("failed to update studio: %w", err)
	}
	return nil
}

// --- Actors ---

func (c *GoonHubClient) ListActors() ([]GHActorListItem, error) {
	var resp struct {
		Data []GHActorListItem `json:"data"`
	}
	if err := c.doWithRetry("GET", "/api/v1/actors?limit=10000", nil, &resp); err != nil {
		return nil, fmt.Errorf("failed to list actors: %w", err)
	}
	return resp.Data, nil
}

func (c *GoonHubClient) CreateActor(req GHCreateActorRequest) (*GHActor, error) {
	var actor GHActor
	if err := c.doWithRetry("POST", "/api/v1/admin/actors", req, &actor); err != nil {
		return nil, fmt.Errorf("failed to create actor: %w", err)
	}
	return &actor, nil
}

// --- Scenes (Import) ---

func (c *GoonHubClient) ImportScene(req GHImportSceneRequest) (*GHImportSceneResponse, error) {
	var resp GHImportSceneResponse
	if err := c.doWithRetry("POST", "/api/v1/admin/import/scenes", req, &resp); err != nil {
		return nil, fmt.Errorf("failed to import scene: %w", err)
	}
	return &resp, nil
}

// --- Scene Associations ---

func (c *GoonHubClient) SetSceneTags(sceneID uint, tagIDs []uint) error {
	path := fmt.Sprintf("/api/v1/scenes/%d/tags", sceneID)
	if err := c.doWithRetry("PUT", path, GHSetTagsRequest{TagIDs: tagIDs}, nil); err != nil {
		return fmt.Errorf("failed to set scene tags: %w", err)
	}
	return nil
}

func (c *GoonHubClient) SetSceneActors(sceneID uint, actorIDs []uint) error {
	path := fmt.Sprintf("/api/v1/scenes/%d/actors", sceneID)
	if err := c.doWithRetry("PUT", path, GHSetActorsRequest{ActorIDs: actorIDs}, nil); err != nil {
		return fmt.Errorf("failed to set scene actors: %w", err)
	}
	return nil
}

func (c *GoonHubClient) SetSceneStudio(sceneID uint, studioID uint) error {
	path := fmt.Sprintf("/api/v1/scenes/%d/studio", sceneID)
	if err := c.doWithRetry("PUT", path, GHSetStudioRequest{StudioID: studioID}, nil); err != nil {
		return fmt.Errorf("failed to set scene studio: %w", err)
	}
	return nil
}

// --- Markers (Import) ---

func (c *GoonHubClient) ImportMarker(req GHImportMarkerRequest) (*GHImportMarkerResponse, error) {
	var resp GHImportMarkerResponse
	if err := c.doWithRetry("POST", "/api/v1/admin/import/markers", req, &resp); err != nil {
		return nil, fmt.Errorf("failed to import marker: %w", err)
	}
	return &resp, nil
}

func (c *GoonHubClient) SetMarkerTags(markerID uint, tagIDs []uint) error {
	path := fmt.Sprintf("/api/v1/markers/%d/tags", markerID)
	if err := c.doWithRetry("PUT", path, GHSetMarkerTagsRequest{TagIDs: tagIDs}, nil); err != nil {
		return fmt.Errorf("failed to set marker tags: %w", err)
	}
	return nil
}

// --- Error types ---

type ConflictError struct {
	Message    string
	ExistingID uint
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict: %s", e.Message)
}

type ServerError struct {
	StatusCode int
	Body       string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server error %d: %s", e.StatusCode, e.Body)
}
