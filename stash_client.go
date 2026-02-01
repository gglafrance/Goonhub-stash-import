package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type StashClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewStashClient(baseURL, apiKey string) *StashClient {
	return &StashClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *StashClient) query(queryStr string, result any) error {
	body := map[string]string{"query": queryStr}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal query: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/graphql", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ApiKey", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graphql request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

func (c *StashClient) FetchTags() ([]StashTag, error) {
	q := `{
		findTags(filter: { per_page: -1 }) {
			tags {
				id
				name
			}
		}
	}`

	var resp graphqlResponse[findTagsData]
	if err := c.query(q, &resp); err != nil {
		return nil, fmt.Errorf("failed to fetch tags: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %s", resp.Errors[0].Message)
	}
	return resp.Data.FindTags.Tags, nil
}

func (c *StashClient) FetchStudios() ([]StashStudio, error) {
	q := `{
		findStudios(filter: { per_page: -1 }) {
			studios {
				id
				name
				urls
				details
				rating100
				parent_studio { id }
			}
		}
	}`

	var resp graphqlResponse[findStudiosData]
	if err := c.query(q, &resp); err != nil {
		return nil, fmt.Errorf("failed to fetch studios: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %s", resp.Errors[0].Message)
	}
	return resp.Data.FindStudios.Studios, nil
}

func (c *StashClient) FetchPerformers() ([]StashPerformer, error) {
	q := `{
		findPerformers(filter: { per_page: -1 }) {
			performers {
				id
				name
				gender
				birthdate
				death_date
				ethnicity
				country
				eye_color
				height_cm
				measurements
				fake_tits
				tattoos
				piercings
				hair_color
				weight
				image_path
			}
		}
	}`

	var resp graphqlResponse[findPerformersData]
	if err := c.query(q, &resp); err != nil {
		return nil, fmt.Errorf("failed to fetch performers: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %s", resp.Errors[0].Message)
	}
	return resp.Data.FindPerformers.Performers, nil
}

func (c *StashClient) FetchScenes() ([]StashScene, error) {
	q := `{
		findScenes(filter: { per_page: -1 }) {
			scenes {
				id
				title
				details
				date
				rating100
				o_counter
				files {
					path
					size
					duration
					width
					height
					video_codec
					audio_codec
					frame_rate
					bit_rate
					basename
				}
				studio { id }
				performers { id }
				tags { id }
				scene_markers {
					id
					title
					seconds
					primary_tag { id }
					tags { id }
				}
			}
		}
	}`

	var resp graphqlResponse[findScenesData]
	if err := c.query(q, &resp); err != nil {
		return nil, fmt.Errorf("failed to fetch scenes: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %s", resp.Errors[0].Message)
	}
	return resp.Data.FindScenes.Scenes, nil
}
