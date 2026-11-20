// Package stash is a READ-ONLY Stash GraphQL client. It refuses to send any
// operation that is not a query: the Stash instance must never be modified.
package stash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"goonhub-stash-importer/internal/httpx"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	retry   httpx.Policy
}

func New(baseURL, apiKey string, timeout time.Duration, retry httpx.Policy) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
		retry:   retry,
	}
}

// ErrNotAQuery is returned when something other than a GraphQL query is attempted.
var ErrNotAQuery = errors.New("refusing to send a non-query GraphQL operation to Stash (read-only)")

var (
	gqlComment   = regexp.MustCompile(`(?m)#.*$`)
	gqlForbidden = regexp.MustCompile(`(?i)\b(mutation|subscription)\b`)
)

// ensureReadOnly accepts only anonymous "{ ... }" or "query ..." documents that
// contain no mutation/subscription keyword anywhere.
func ensureReadOnly(q string) error {
	s := strings.TrimSpace(gqlComment.ReplaceAllString(q, ""))
	if !(strings.HasPrefix(s, "{") || strings.HasPrefix(s, "query")) || gqlForbidden.MatchString(s) {
		return ErrNotAQuery
	}
	return nil
}

type gqlError struct {
	Message string `json:"message"`
}

// Query runs a read-only GraphQL query and decodes "data" into out.
func (c *Client) Query(ctx context.Context, query string, vars map[string]any, out any) error {
	if err := ensureReadOnly(query); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("failed to marshal query: %w", err)
	}
	return c.retry.Do(ctx, true, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphql", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("ApiKey", c.apiKey)
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("stash request failed: %w", err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read stash response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return &httpx.StatusError{Method: "POST", URL: c.baseURL + "/graphql", Status: resp.StatusCode, Body: string(raw)}
		}
		var env struct {
			Data   json.RawMessage `json:"data"`
			Errors []gqlError      `json:"errors"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("failed to decode stash response: %w", err)
		}
		if len(env.Errors) > 0 {
			msgs := make([]string, len(env.Errors))
			for i, e := range env.Errors {
				msgs[i] = e.Message
			}
			return fmt.Errorf("stash graphql error: %s", strings.Join(msgs, "; "))
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("failed to decode stash data: %w", err)
		}
		return nil
	})
}

// Download GETs a Stash asset URL (image, screenshot). The URL host returned by
// Stash reflects the request host, so it is rewritten to the configured base URL.
// It returns (nil, "", nil) for Stash's generated default images.
func (c *Client) Download(ctx context.Context, assetURL string) ([]byte, string, error) {
	u, err := url.Parse(assetURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid asset url %q: %w", assetURL, err)
	}
	if u.Query().Get("default") == "true" {
		return nil, "", nil
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid stash url: %w", err)
	}
	u.Scheme, u.Host = base.Scheme, base.Host
	var data []byte
	var ctype string
	err = c.retry.Do(ctx, true, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("ApiKey", c.apiKey)
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("stash download failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			data = nil
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return &httpx.StatusError{Method: "GET", URL: u.String(), Status: resp.StatusCode, Body: string(b)}
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		ctype = resp.Header.Get("Content-Type")
		return err
	})
	return data, ctype, err
}
