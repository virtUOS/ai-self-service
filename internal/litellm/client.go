package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL   string
	masterKey string
	http      *http.Client

	// mu guards the cached pricing, which is refreshed in the background while
	// requests read it.
	mu      sync.RWMutex
	pricing Pricing
}

func NewClient(baseURL, masterKey string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		masterKey: masterKey,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// RefreshPricing re-reads what the gateway charges per token.
//
// The price is cached rather than fetched per conversion: it changes only when
// an operator edits a model, and a quota calculation must not depend on the
// gateway answering. A failed refresh leaves the previous value in place.
func (c *Client) RefreshPricing(ctx context.Context) error {
	p, err := c.Pricing(ctx)
	if err != nil {
		return err
	}
	// A gateway with no priced model would otherwise set the rate to zero,
	// making every quota cost nothing and so never bind.
	if p.TokenPrice <= 0 {
		return nil
	}
	c.mu.Lock()
	c.pricing = p
	c.mu.Unlock()
	return nil
}

// CurrentPricing is the cached view of what the gateway charges, for callers
// that want to report on it rather than convert with it.
func (c *Client) CurrentPricing() Pricing {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pricing
}

// tokenPrice is the rate conversions use: whatever the gateway last reported,
// falling back to the nominal rate before the first successful refresh. A
// dashboard that cannot price a quota is worse than one priced at the rate the
// deployment is expected to use.
func (c *Client) tokenPrice() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.pricing.TokenPrice > 0 {
		return c.pricing.TokenPrice
	}
	return NominalTokenPrice
}

// TokensToBudget converts a token allowance into the spend cap LiteLLM
// enforces, at the price the gateway actually charges.
func (c *Client) TokensToBudget(tokens int64) float64 {
	return float64(tokens) * c.tokenPrice()
}

// BudgetToTokens is the inverse, for showing an upstream spend figure back in
// the units an admin configured.
func (c *Client) BudgetToTokens(budget float64) int64 {
	return int64(budget / c.tokenPrice())
}

// BudgetWindow is one allowance window as LiteLLM's API expects it.
type BudgetWindow struct {
	BudgetDuration string  `json:"budget_duration"`
	MaxBudget      float64 `json:"max_budget"`
}

type KeyParams struct {
	KeyAlias       string         `json:"key_alias,omitempty"`
	Models         []string       `json:"models,omitempty"`
	TPMLimit       *int64         `json:"tpm_limit,omitempty"`
	RPMLimit       *int64         `json:"rpm_limit,omitempty"`
	MaxBudget      *float64       `json:"max_budget,omitempty"`
	BudgetDuration *string        `json:"budget_duration,omitempty"`
	BudgetLimits   []BudgetWindow `json:"budget_limits,omitempty"`
	UserID         string         `json:"user_id,omitempty"`
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

type modelInfoResponse struct {
	Data []struct {
		ModelName string `json:"model_name"`
		ModelInfo struct {
			// Mode is how the gateway classifies the model. It is absent or
			// null for several models, which is why nothing may treat a
			// missing mode as meaningful.
			Mode string `json:"mode"`
		} `json:"model_info"`
	} `json:"data"`
}

// EmbeddingModels is the set of models the gateway serves as embedding models.
//
// An embedding model takes a different endpoint and body than a chat model, so
// the dashboard's example request has to know which it is showing. The gateway
// is asked rather than the name inspected: "bge-m3" reads as an embedding model
// to a human, but nothing guarantees the next one will.
//
// A model whose mode is missing or null is deliberately NOT reported here.
// Several models report no mode at all, and a chat example is the safe default
// — it is what nearly every model on this gateway is.
func (c *Client) EmbeddingModels(ctx context.Context) (map[string]bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/model/info", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LiteLLM /model/info returned %d: %s", resp.StatusCode, b)
	}

	var result modelInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}

	out := make(map[string]bool)
	for _, m := range result.Data {
		if m.ModelName != "" && m.ModelInfo.Mode == "embedding" {
			out[m.ModelName] = true
		}
	}
	return out, nil
}

// ListModels returns the model names the gateway serves.
//
// /model/info is used rather than /v1/models: the portal's credential is
// scoped to management routes, and /v1/models reflects that key's own model
// access rather than everything configured on the proxy.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/model/info", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LiteLLM /model/info returned %d: %s", resp.StatusCode, b)
	}

	var result modelInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}

	seen := make(map[string]bool, len(result.Data))
	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ModelName != "" && !seen[m.ModelName] {
			seen[m.ModelName] = true
			models = append(models, m.ModelName)
		}
	}
	sort.Strings(models)
	return models, nil
}
