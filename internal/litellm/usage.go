package litellm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// spendRow is one request in LiteLLM's spend log.
//
// Only the fields the portal aggregates are declared; the row carries plenty
// more (latency, cache hits, the request body) that nothing here needs.
type spendRow struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
	// ModelGroup is the public model name a request was made with; Model is
	// the deployment it was routed to, with its provider prefix.
	ModelGroup       string  `json:"model_group"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Spend            float64 `json:"spend"`
	StartTime        string  `json:"startTime"`
}

// keyHash is how LiteLLM identifies a key in its spend log: the SHA-256 of the
// key itself. The portal stores the key, so it derives this rather than
// keeping a second copy of the same fact.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// History returns what a key consumed over the last days days: per-day token
// totals and a per-model breakdown, both from a single read of the log.
//
// The log is fetched once because it cannot be narrowed server-side: LiteLLM
// ignores page_size, limit and size on this route, and passing
// start_date/end_date switches the response to an aggregated shape that
// reports spend only and drops token counts entirely — and local models are
// priced so that spend is always zero, so it carries no usable signal. The
// window is therefore bounded here, over rows already in hand.
func (c *Client) History(ctx context.Context, key string, days int) (keyprovider.History, error) {
	rows, err := c.spendLog(ctx, key)
	if err != nil {
		return keyprovider.History{}, err
	}
	cutoff := dayCutoff(days)
	return keyprovider.History{
		Days:   dailyFromRows(rows, cutoff),
		Models: modelsFromRows(rows, cutoff),
	}, nil
}

// dayCutoff is the earliest UTC date to count, as a YYYY-MM-DD prefix.
//
// A date-string compare is the right granularity here: the chart and the
// per-model table are both bucketed by UTC day, so a row either belongs to a
// counted day or it does not. spentSince in windows.go is the other case — it
// bounds an exact window start, so it parses the timestamp instead.
func dayCutoff(days int) string {
	return time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
}

// dailyFromRows sums tokens per UTC day for rows at or after cutoff, oldest
// day first.
func dailyFromRows(rows []spendRow, cutoff string) []keyprovider.DailyUsage {
	totals := make(map[string]int64)
	for _, r := range rows {
		if r.TotalTokens <= 0 || len(r.StartTime) < 10 {
			continue
		}
		day := r.StartTime[:10]
		if day < cutoff {
			continue
		}
		totals[day] += r.TotalTokens
	}

	out := make([]keyprovider.DailyUsage, 0, len(totals))
	for day, tokens := range totals {
		out = append(out, keyprovider.DailyUsage{Day: day, Tokens: tokens})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// modelsFromRows sums per-model totals for rows at or after cutoff, largest
// total first.
func modelsFromRows(rows []spendRow, cutoff string) []keyprovider.ModelUsage {
	totals := make(map[string]*keyprovider.ModelUsage)
	for _, r := range rows {
		if r.TotalTokens <= 0 || len(r.StartTime) < 10 || r.StartTime[:10] < cutoff {
			continue
		}
		// Label by the public name the request was made with, not the
		// deployment it was routed to: users know "Qwen/…", not "openai/Qwen/…".
		name := r.ModelGroup
		if name == "" {
			name = r.Model
		}
		m, ok := totals[name]
		if !ok {
			m = &keyprovider.ModelUsage{Model: name}
			totals[name] = m
		}
		m.Requests++
		m.PromptTokens += r.PromptTokens
		m.CompletionTokens += r.CompletionTokens
		m.TotalTokens += r.TotalTokens
	}

	out := make([]keyprovider.ModelUsage, 0, len(totals))
	for _, m := range totals {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Model < out[j].Model
	})
	return out
}

type keyInfoResponse struct {
	Info struct {
		Spend         float64  `json:"spend"`
		MaxBudget     *float64 `json:"max_budget"`
		BudgetResetAt *string  `json:"budget_reset_at"`
	} `json:"info"`
}

// keyInfo fetches the gateway's own record of a key.
func (c *Client) keyInfo(ctx context.Context, key string) (keyInfoResponse, error) {
	var info keyInfoResponse

	q := url.Values{}
	q.Set("key", key)

	resp, err := c.do(ctx, http.MethodGet, "/key/info?"+q.Encode(), nil)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return info, fmt.Errorf("LiteLLM /key/info returned %d: %s", resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, fmt.Errorf("decode key info: %w", err)
	}
	return info, nil
}

// KeyQuota reports spend against the key's enforced budget, both read from
// the key itself: that is the counter LiteLLM enforces against, and it resets
// on the budget period rather than the 30-day window the log is charted over.
func (c *Client) KeyQuota(ctx context.Context, key string) (keyprovider.Quota, error) {
	info, err := c.keyInfo(ctx, key)
	if err != nil {
		return keyprovider.Quota{}, err
	}
	q := keyprovider.Quota{Used: info.Info.Spend}
	if info.Info.MaxBudget != nil {
		q.Limit = *info.Info.MaxBudget
	}
	if info.Info.BudgetResetAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.Info.BudgetResetAt); err == nil {
			q.ResetsAt = t
		}
	}
	return q, nil
}

// KeySpend is the cumulative spend recorded on the key itself. It is the
// fallback for when per-request spend logging is switched off: the key's
// counter keeps working either way, at the cost of any per-day breakdown.
func (c *Client) KeySpend(ctx context.Context, key string) (float64, error) {
	info, err := c.keyInfo(ctx, key)
	if err != nil {
		return 0, err
	}
	return info.Info.Spend, nil
}

// UpdateKeyLimits pushes a profile's limits onto a key that already exists.
//
// Creating a key is not the only time its limits change: a user can be moved
// between profiles, and a profile's quota can be edited. Without this the
// portal would advertise a limit the gateway does not enforce.
//
// Fields are sent explicitly rather than omitted when empty. LiteLLM leaves an
// omitted field untouched, so clearing a quota has to send null — otherwise a
// profile that loses its allowance keeps enforcing the previous one.
func (c *Client) UpdateKeyLimits(ctx context.Context, key string, l keyprovider.Limits) error {
	payload := map[string]any{
		"key":       key,
		"tpm_limit": l.TokensPerMinute,
		"rpm_limit": l.RequestsPerMinute,
	}

	// An unrestricted profile sends an empty list, not null. /key/update
	// rejects null with "A value is required but not set" — unlike
	// /key/generate, where omitempty drops the field before it reaches the API.
	//
	// Empty must be sent rather than omitted, or a profile that drops its
	// restriction would never clear the old list. Verified against the live
	// gateway: [] clears a restriction, and a key with [] serves every model.
	models := l.Models
	if models == nil {
		models = []string{}
	}
	payload["models"] = models

	// Both shapes are always sent, one of them null: LiteLLM leaves an omitted
	// field untouched, so a key moving between shapes would otherwise keep
	// enforcing the one it no longer uses.
	windows := effectiveWindows(l)
	switch {
	case len(windows) > 1:
		// Several windows: budget_limits, enforced independently upstream.
		limits := make([]map[string]any, 0, len(windows))
		for _, w := range windows {
			limits = append(limits, map[string]any{
				"budget_duration": w.Period,
				"max_budget":      w.Budget,
			})
		}
		payload["budget_limits"] = limits
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
	case len(windows) == 1:
		// One window: the plain pair already works, so leave it alone.
		payload["max_budget"] = windows[0].Budget
		payload["budget_duration"] = windows[0].Period
		payload["budget_limits"] = nil
	default:
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
		payload["budget_limits"] = nil
	}

	body, err := json.Marshal(payload)
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

// effectiveWindows drops windows that are not actually limits, so a blank row
// left in the admin form does not become a zero-budget quota upstream.
func effectiveWindows(l keyprovider.Limits) []keyprovider.QuotaWindow {
	out := make([]keyprovider.QuotaWindow, 0, len(l.Quotas))
	for _, q := range l.Quotas {
		if q.Budget > 0 && q.Period != "" {
			out = append(out, q)
		}
	}
	return out
}
