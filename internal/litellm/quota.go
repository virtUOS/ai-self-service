package litellm

import (
	"fmt"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// NominalTokenPrice is the synthetic per-token price configured on the local
// models in LiteLLM. Input and output are priced identically, which is what
// makes a token quota exact: cost is strictly proportional to total tokens, so
// TokensToBudget is not sensitive to the input/output mix of real traffic.
//
// This MUST match input_cost_per_token / output_cost_per_token on every model
// the portal issues keys for. A model left at 0 or null accrues no spend, and
// a quota over it can never trigger.
const NominalTokenPrice = 1e-07

// ValidQuotaPeriods are the reset windows LiteLLM accepts for budget_duration.
// They reset on fixed boundaries (24h at midnight UTC, 7d weekly, 30d monthly)
// rather than as a sliding window.
var ValidQuotaPeriods = []string{"1h", "24h", "7d", "30d"}

// IsValidQuotaPeriod reports whether p is a period the upstream understands.
// The empty string is valid and means "no quota".
func IsValidQuotaPeriod(p string) bool {
	if p == "" {
		return true
	}
	for _, v := range ValidQuotaPeriods {
		if v == p {
			return true
		}
	}
	return false
}

// TokensToBudget converts a token allowance into the spend cap LiteLLM
// enforces. Admins configure tokens; only this function knows about the price.
func TokensToBudget(tokens int64) float64 {
	return float64(tokens) * NominalTokenPrice
}

// BudgetToTokens is the inverse, for displaying an upstream spend figure back
// to an admin in the units they configured.
func BudgetToTokens(budget float64) int64 {
	return int64(budget / NominalTokenPrice)
}

// FormatTokens renders a token count compactly for the UI (1500000 -> "1.5M").
func FormatTokens(tokens int64) string {
	switch {
	case tokens <= 0:
		return "unlimited"
	case tokens >= 1_000_000:
		return trimZero(float64(tokens)/1_000_000) + "M"
	case tokens >= 1_000:
		return trimZero(float64(tokens)/1_000) + "k"
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

func trimZero(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}

// periodRank orders the reset windows from tightest to widest. The order is
// explicit rather than derived from the string, because "7d" and "30d" do not
// sort lexicographically and a wrong order would silently put the wrong
// allowance on the user.
func periodRank(period string) int {
	switch period {
	case "1h":
		return 1
	case "24h":
		return 2
	case "7d":
		return 3
	case "30d":
		return 4
	}
	return 0
}

// WidestWindow splits a profile's windows into the one enforced on the user
// and the ones left on the key.
//
// The widest window is the allowance worth rotating a key to escape, so that
// is the one that has to follow the person (issue #26). The shorter windows
// stay on the key: they cap bursts and reset on their own within hours, so a
// rotation that resets them gains nothing worth defending against. An internal
// user can hold only one window — it accepts budget_limits and silently drops
// them — which is why this is a split rather than a copy.
//
// Returns the widest window and the remainder, preserving input order. For no
// windows the widest is the zero value, which callers read as "no budget".
func WidestWindow(windows []keyprovider.QuotaWindow) (keyprovider.QuotaWindow, []keyprovider.QuotaWindow) {
	if len(windows) == 0 {
		return keyprovider.QuotaWindow{}, nil
	}

	widest := 0
	for i, w := range windows[1:] {
		if periodRank(w.Period) > periodRank(windows[widest].Period) {
			widest = i + 1
		}
	}

	rest := make([]keyprovider.QuotaWindow, 0, len(windows)-1)
	for i, w := range windows {
		if i != widest {
			rest = append(rest, w)
		}
	}
	return windows[widest], rest
}
