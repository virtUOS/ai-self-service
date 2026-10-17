package litellm

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
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
	Spend float64
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
//
// LiteLLM reports the spend of every window in budget_limits. A window without
// a figure is an error rather than a zero: a bar drawn from a silent zero
// would promise an allowance the user may not have.
func (c *Client) keyWindows(ctx context.Context, ref string) ([]budgetWindow, error) {
	info, err := c.keyInfo(ctx, ref)
	if err != nil {
		return nil, err
	}

	if windows := info.Info.BudgetLimits; len(windows) > 0 {
		for i := range windows {
			u, ok := info.Info.BudgetLimitsUsage[windows[i].BudgetDuration]
			if !ok {
				return nil, fmt.Errorf("LiteLLM /key/info reports no spend for the %s window", windows[i].BudgetDuration)
			}
			windows[i].Spend = u.CurrentSpend
		}
		return windows, nil
	}
	if info.Info.MaxBudget != nil && info.Info.BudgetDuration != nil {
		return []budgetWindow{{
			BudgetDuration: *info.Info.BudgetDuration,
			MaxBudget:      *info.Info.MaxBudget,
			ResetAt:        info.Info.BudgetResetAt,
			Spend:          info.Info.Spend,
		}}, nil
	}
	return nil, nil
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
			Period: w.BudgetDuration,
			Limit:  w.MaxBudget,
			Used:   w.Spend,
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
