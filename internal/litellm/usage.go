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
// more (model, latency, prompt/completion split) that nothing here needs.
type spendRow struct {
	APIKey      string `json:"api_key"`
	TotalTokens int64  `json:"total_tokens"`
	StartTime   string `json:"startTime"`
}

// keyHash is how LiteLLM identifies a key in its spend log: the SHA-256 of the
// key itself. The portal stores the key, so it derives this rather than
// keeping a second copy of the same fact.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Usage returns per-day token totals for a key, oldest first.
//
// It reads the raw per-request log and aggregates here. LiteLLM will aggregate
// by day itself when given start_date/end_date, but that response reports
// spend only and drops token counts entirely — and local models are priced so
// that spend is always zero, so it carries no usable signal.
func (c *Client) Usage(ctx context.Context, key string, days int) ([]keyprovider.DailyUsage, error) {
	q := url.Values{}
	q.Set("api_key", keyHash(key))

	resp, err := c.do(ctx, http.MethodGet, "/spend/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LiteLLM /spend/logs returned %d: %s", resp.StatusCode, b)
	}

	var rows []spendRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode spend log: %w", err)
	}

	// Bound the window here rather than in the query: start_date/end_date
	// switch the endpoint to its aggregated shape, which has no token counts.
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

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
	return out, nil
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

// KeyQuota reports consumption against the key's enforced budget.
//
// Both figures come from the key itself rather than the per-request log: this
// is the counter LiteLLM actually enforces against, and it resets on the
// budget period, which rarely matches the 30-day window the log is charted
// over. Summing the log would give a number that disagrees with the limit
// users actually hit.
func (c *Client) KeyQuota(ctx context.Context, key string) (keyprovider.Quota, error) {
	info, err := c.keyInfo(ctx, key)
	if err != nil {
		return keyprovider.Quota{}, err
	}

	q := keyprovider.Quota{UsedTokens: BudgetToTokens(info.Info.Spend)}
	if info.Info.MaxBudget != nil {
		q.LimitTokens = BudgetToTokens(*info.Info.MaxBudget)
	}
	if info.Info.BudgetResetAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.Info.BudgetResetAt); err == nil {
			q.ResetsAt = t
		}
	}
	return q, nil
}

// KeySpendTokens is the cumulative token usage recorded on the key itself,
// derived from the spend LiteLLM tracks per key.
//
// This is the fallback for when per-request spend logging is switched off.
// It was disabled on this deployment to bound a LiteLLM memory leak
// (BerriAI/litellm#12685), and the portal cannot assume it is ever on: the
// key's own spend counter keeps working either way. The cost is granularity —
// one cumulative figure for the key's lifetime, with no per-day breakdown.
func (c *Client) KeySpendTokens(ctx context.Context, key string) (int64, error) {
	info, err := c.keyInfo(ctx, key)
	if err != nil {
		return 0, err
	}
	return BudgetToTokens(info.Info.Spend), nil
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

	if l.QuotaTokens > 0 && l.QuotaPeriod != "" {
		payload["max_budget"] = TokensToBudget(l.QuotaTokens)
		payload["budget_duration"] = l.QuotaPeriod
	} else {
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
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
