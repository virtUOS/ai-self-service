package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL   string
	masterKey string
	http      *http.Client
}

func NewClient(baseURL, masterKey string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		masterKey: masterKey,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

type KeyParams struct {
	KeyAlias       string         `json:"key_alias,omitempty"`
	Models         []string       `json:"models,omitempty"`
	TPMLimit       *int64         `json:"tpm_limit,omitempty"`
	RPMLimit       *int64         `json:"rpm_limit,omitempty"`
	MaxBudget      *float64       `json:"max_budget,omitempty"`
	BudgetDuration *string        `json:"budget_duration,omitempty"`
	Duration       string         `json:"duration,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type createKeyResponse struct {
	Key string `json:"key"`
}

func (c *Client) CreateKey(ctx context.Context, alias string, params KeyParams, expiresAt time.Time) (string, error) {
	params.KeyAlias = alias
	params.Duration = fmt.Sprintf("%dd", int(time.Until(expiresAt).Hours()/24)+1)

	body, err := json.Marshal(params)
	if err != nil {
		return "", err
	}

	resp, err := c.do(ctx, http.MethodPost, "/key/generate", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LiteLLM /key/generate returned %d: %s", resp.StatusCode, b)
	}

	var result createKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Key == "" {
		return "", fmt.Errorf("LiteLLM returned empty key")
	}
	return result.Key, nil
}

func (c *Client) DeleteKey(ctx context.Context, key string) error {
	body, err := json.Marshal(map[string]any{"keys": []string{key}})
	if err != nil {
		return err
	}

	resp, err := c.do(ctx, http.MethodPost, "/key/delete", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LiteLLM /key/delete returned %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *Client) UpdateKeyExpiry(ctx context.Context, key string, expiresAt time.Time) error {
	body, err := json.Marshal(map[string]any{
		"key":      key,
		"duration": fmt.Sprintf("%dd", int(time.Until(expiresAt).Hours()/24)+1),
	})
	if err != nil {
		return err
	}

	resp, err := c.do(ctx, http.MethodPost, "/key/update", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LiteLLM /key/update returned %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}
