package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// budgetWindow is one entry of LiteLLM's budget_limits array.
//
// It carries the allowance and when the window next rolls over, but not what
// has been spent inside it: the gateway keeps a counter per window and does
// not expose one. Consumption is therefore summed from the spend log.
type budgetWindow struct {
	BudgetDuration string  `json:"budget_duration"`
	MaxBudget      float64 `json:"max_budget"`
	ResetAt        *string `json:"reset_at"`
}

// periodDuration is how long a reset window lasts. Used to step back from the
// gateway's reset_at to the moment the window opened, rather than guessing at
// calendar boundaries — LiteLLM resets on fixed points (midnight UTC, Monday,
// the 1st), and reset_at already encodes which one applies.
func periodDuration(period string) time.Duration {
	switch period {
	case "1h":
		return time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 0
}

// keyWindows reads the stacked windows configured on a key.
func (c *Client) keyWindows(ctx context.Context, key string) ([]budgetWindow, error) {
	q := url.Values{}
	q.Set("key", key)

	resp, err := c.do(ctx, http.MethodGet, "/key/info?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LiteLLM /key/info returned %d: %s", resp.StatusCode, b)
	}

	var body struct {
		Info struct {
			BudgetLimits   []budgetWindow `json:"budget_limits"`
			MaxBudget      *float64       `json:"max_budget"`
			BudgetDuration *string        `json:"budget_duration"`
			BudgetResetAt  *string        `json:"budget_reset_at"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode key info: %w", err)
	}

	if len(body.Info.BudgetLimits) > 0 {
		return body.Info.BudgetLimits, nil
	}
	// A single window is held in the flat pair rather than the array.
	if body.Info.MaxBudget != nil && body.Info.BudgetDuration != nil {
		return []budgetWindow{{
			BudgetDuration: *body.Info.BudgetDuration,
			MaxBudget:      *body.Info.MaxBudget,
			ResetAt:        body.Info.BudgetResetAt,
		}}, nil
	}
	return nil, nil
}

// spentSince sums the tokens a key consumed at or after start.
//
// Returns known=false when the gateway keeps no per-request log, which is a
// deployment choice rather than an error: production disables it to bound a
// memory leak. A zero total is then not evidence of no usage, and the caller
// must not draw a bar from it.
func (c *Client) spentSince(ctx context.Context, key string, start time.Time) (tokens int64, known bool, err error) {
	q := url.Values{}
	q.Set("api_key", keyHash(key))

	resp, err := c.do(ctx, http.MethodGet, "/spend/logs?"+q.Encode(), nil)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return 0, false, fmt.Errorf("LiteLLM /spend/logs returned %d: %s", resp.StatusCode, b)
	}

	var rows []spendRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return 0, false, fmt.Errorf("decode spend log: %w", err)
	}
	if len(rows) == 0 {
		// Indistinguishable from logging being switched off, so report the
		// figure as unknown rather than claiming a full allowance.
		return 0, false, nil
	}

	for _, r := range rows {
		if r.TotalTokens <= 0 {
			continue
		}
		t, err := time.Parse(time.RFC3339, r.StartTime)
		if err != nil {
			continue
		}
		if !t.Before(start) {
			tokens += r.TotalTokens
		}
	}
	return tokens, true, nil
}

// Windows reports consumption against every quota window applying to a key and
// its owner, tightest period first.
//
// The windows live in two places since issue #26: the widest is enforced on
// the owner so it survives a key rotation, and the shorter ones stay on the
// key. Both are gathered here so the dashboard can show what actually binds.
func (p *Provider) Windows(ctx context.Context, ref, ownerID string) ([]keyprovider.WindowUsage, error) {
	windows, err := p.client.keyWindows(ctx, ref)
	if err != nil {
		return nil, err
	}

	// The owner's allowance is a window too, and the widest one at that.
	if ownerID != "" {
		owner, err := p.client.userWindow(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		if owner != nil {
			windows = append(windows, *owner)
		}
	}
	if len(windows) == 0 {
		return nil, nil
	}

	out := make([]keyprovider.WindowUsage, 0, len(windows))
	for _, w := range windows {
		if w.MaxBudget <= 0 || w.BudgetDuration == "" {
			continue
		}
		u := keyprovider.WindowUsage{
			Period:      w.BudgetDuration,
			LimitTokens: BudgetToTokens(w.MaxBudget),
		}
		if w.ResetAt != nil {
			if t, err := time.Parse(time.RFC3339, *w.ResetAt); err == nil {
				u.ResetsAt = t
				// The window opened one period before it next resets.
				if d := periodDuration(w.BudgetDuration); d > 0 {
					used, known, err := p.client.spentSince(ctx, ref, t.Add(-d))
					if err != nil {
						return nil, err
					}
					u.UsedTokens, u.UsedKnown = used, known
				}
			}
		}
		out = append(out, u)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return periodRank(out[i].Period) < periodRank(out[j].Period)
	})
	return out, nil
}
