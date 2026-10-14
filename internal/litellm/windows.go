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

// budgetWindow is one entry of LiteLLM's budget_limits array, or the owner's
// allowance in the same shape.
type budgetWindow struct {
	BudgetDuration string  `json:"budget_duration"`
	MaxBudget      float64 `json:"max_budget"`
	ResetAt        *string `json:"reset_at"`

	// Spend is what the gateway has counted against this window since it last
	// reset — the same counter it enforces against. The array itself does not
	// carry it, so keyWindows and userWindow fill it in from the figures
	// reported next to it.
	//
	// HasSpend is false when the gateway reported no figure for the window.
	// That should not happen, but a bar drawn from a silent zero would promise
	// an allowance the user may not have.
	Spend    float64
	HasSpend bool
}

// windowSpend is one entry of /key/info's budget_limits_usage, which LiteLLM
// reports per budget_duration since 1.105.
type windowSpend struct {
	CurrentSpend float64 `json:"current_spend"`
}

// keyWindows reads the windows configured on a key, each with what has been
// spent against it.
//
// A key holds its windows in one of two shapes. Several go in budget_limits,
// with their spend in budget_limits_usage. A single one is kept in the flat
// max_budget/budget_duration pair, and the key's own spend counter then covers
// just that window: LiteLLM resets it whenever the window rolls over.
func (c *Client) keyWindows(ctx context.Context, ref string) ([]budgetWindow, error) {
	resp, err := c.do(ctx, http.MethodGet, keyInfoPath(ref), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, keyCallError("/key/info", resp)
	}

	var body struct {
		Info struct {
			Status            string                 `json:"status"`
			Spend             float64                `json:"spend"`
			BudgetLimits      []budgetWindow         `json:"budget_limits"`
			BudgetLimitsUsage map[string]windowSpend `json:"budget_limits_usage"`
			MaxBudget         *float64               `json:"max_budget"`
			BudgetDuration    *string                `json:"budget_duration"`
			BudgetResetAt     *string                `json:"budget_reset_at"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode key info: %w", err)
	}
	if body.Info.Status == keyStatusDeleted {
		return nil, fmt.Errorf("LiteLLM /key/info reports the key deleted: %w", keyprovider.ErrKeyNotFound)
	}

	if len(body.Info.BudgetLimits) > 0 {
		windows := body.Info.BudgetLimits
		for i := range windows {
			if u, ok := body.Info.BudgetLimitsUsage[windows[i].BudgetDuration]; ok {
				windows[i].Spend, windows[i].HasSpend = u.CurrentSpend, true
			}
		}
		return windows, nil
	}
	if body.Info.MaxBudget != nil && body.Info.BudgetDuration != nil {
		return []budgetWindow{{
			BudgetDuration: *body.Info.BudgetDuration,
			MaxBudget:      *body.Info.MaxBudget,
			ResetAt:        body.Info.BudgetResetAt,
			Spend:          body.Info.Spend,
			HasSpend:       true,
		}}, nil
	}
	return nil, nil
}

// spendLog fetches a key's per-request log, for History.
//
// The whole log comes back whatever is asked for: LiteLLM ignores page_size,
// limit and size on this route, and passing start_date/end_date switches the
// response to an aggregated shape that drops token counts entirely.
func (c *Client) spendLog(ctx context.Context, ref string) ([]spendRow, error) {
	q := url.Values{}
	q.Set("api_key", ref)

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

// Windows reports consumption against every quota window applying to a key and
// its owner, tightest period first.
//
// The windows live in two places since issue #26: the widest is enforced on
// the owner so it survives a key rotation, and the shorter ones stay on the
// key. Both are gathered here so the dashboard can show what actually binds.
//
// Every figure is the gateway's own counter for that window, read alongside
// the window itself, so what the dashboard shows is what the gateway enforces.
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

	out := make([]keyprovider.WindowUsage, 0, len(windows))
	for _, w := range windows {
		if w.MaxBudget <= 0 || w.BudgetDuration == "" {
			continue
		}
		u := keyprovider.WindowUsage{
			Period:    w.BudgetDuration,
			Limit:     w.MaxBudget,
			Used:      w.Spend,
			UsedKnown: w.HasSpend,
		}
		if w.ResetAt != nil {
			if t, err := time.Parse(time.RFC3339, *w.ResetAt); err == nil {
				u.ResetsAt = t
			}
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, nil
	}

	sort.SliceStable(out, func(i, j int) bool {
		return periodRank(out[i].Period) < periodRank(out[j].Period)
	})
	return out, nil
}
