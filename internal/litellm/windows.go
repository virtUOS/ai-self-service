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

	// Spend is consumption the gateway itself tracks against this window, set
	// only for the owner's window. The key's windows have no such counter
	// exposed, so theirs is derived from the spend log instead.
	//
	// It matters because the owner's window outlives the key: after a rotation
	// the new key's log is empty, and deriving the owner's usage from it would
	// draw the widest window at zero and imply the allowance had reset.
	Spend    float64
	HasSpend bool
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
func (c *Client) keyWindows(ctx context.Context, key string) (windows []budgetWindow, spend float64, err error) {
	q := url.Values{}
	q.Set("key", key)

	resp, err := c.do(ctx, http.MethodGet, "/key/info?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("LiteLLM /key/info returned %d: %s", resp.StatusCode, b)
	}

	var body struct {
		Info struct {
			Spend          float64        `json:"spend"`
			BudgetLimits   []budgetWindow `json:"budget_limits"`
			MaxBudget      *float64       `json:"max_budget"`
			BudgetDuration *string        `json:"budget_duration"`
			BudgetResetAt  *string        `json:"budget_reset_at"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, fmt.Errorf("decode key info: %w", err)
	}

	if len(body.Info.BudgetLimits) > 0 {
		return body.Info.BudgetLimits, body.Info.Spend, nil
	}
	// A single window is held in the flat pair rather than the array.
	if body.Info.MaxBudget != nil && body.Info.BudgetDuration != nil {
		return []budgetWindow{{
			BudgetDuration: *body.Info.BudgetDuration,
			MaxBudget:      *body.Info.MaxBudget,
			ResetAt:        body.Info.BudgetResetAt,
		}}, body.Info.Spend, nil
	}
	return nil, body.Info.Spend, nil
}

// spendLog fetches a key's per-request log.
//
// The whole log comes back whatever is asked for: LiteLLM ignores page_size,
// limit and size on this route, and passing start_date/end_date switches the
// response to an aggregated shape that drops token counts entirely. So it is
// fetched once and every window counted from it, rather than once per window —
// three windows meant three identical multi-megabyte downloads per page load.
func (c *Client) spendLog(ctx context.Context, key string) ([]spendRow, error) {
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
	return rows, nil
}

// spentSince sums the tokens consumed at or after start, from an already
// fetched log.
func spentSince(rows []spendRow, start time.Time) int64 {
	var tokens int64
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
	return tokens
}

// Windows reports consumption against every quota window applying to a key and
// its owner, tightest period first.
//
// The windows live in two places since issue #26: the widest is enforced on
// the owner so it survives a key rotation, and the shorter ones stay on the
// key. Both are gathered here so the dashboard can show what actually binds.
func (p *Provider) Windows(ctx context.Context, ref, ownerID string) ([]keyprovider.WindowUsage, error) {
	windows, keySpend, err := p.client.keyWindows(ctx, ref)
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

	// One fetch for every window: the log cannot be narrowed server-side, so
	// asking three times would download the same megabytes three times.
	rows, err := p.client.spendLog(ctx, ref)
	if err != nil {
		return nil, err
	}
	// An empty log usually means logging is switched off, which production
	// does deliberately — a zero would then not be evidence of no usage, and a
	// bar drawn from it would promise an allowance the user may not have.
	//
	// The key's own spend counter settles which it is: the gateway maintains
	// that whether or not per-request logging is on, so a key that has spent
	// nothing really has consumed nothing, and its windows can be shown at
	// zero instead of hidden. That is the common case for a key just issued.
	known := len(rows) > 0 || keySpend == 0

	out := make([]keyprovider.WindowUsage, 0, len(windows))
	for _, w := range windows {
		if w.MaxBudget <= 0 || w.BudgetDuration == "" {
			continue
		}
		u := keyprovider.WindowUsage{
			Period:      w.BudgetDuration,
			LimitTokens: p.client.BudgetToTokens(w.MaxBudget),
		}
		if w.ResetAt != nil {
			if t, err := time.Parse(time.RFC3339, *w.ResetAt); err == nil {
				u.ResetsAt = t
				// The window opened one period before it next resets.
				if d := periodDuration(w.BudgetDuration); d > 0 {
					u.UsedTokens = spentSince(rows, t.Add(-d))
					u.UsedKnown = known
				}
			}
		}
		// The owner's window carries its own counter, which survives the key.
		// Prefer it over anything derived from the key's log, and trust it
		// even when logging is off: the gateway maintains it regardless.
		if w.HasSpend {
			u.UsedTokens = p.client.BudgetToTokens(w.Spend)
			u.UsedKnown = true
		}
		out = append(out, u)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return periodRank(out[i].Period) < periodRank(out[j].Period)
	})
	return out, nil
}
