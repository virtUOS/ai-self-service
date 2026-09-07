package litellm

import (
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// Quota windows are budgets: the spend a key may accrue per period, in
// whatever unit the gateway prices its models. LiteLLM enforces spend, so
// passing the budget through unchanged is the only exact option once models
// are priced differently.

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
