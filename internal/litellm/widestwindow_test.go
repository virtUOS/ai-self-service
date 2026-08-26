package litellm

import (
	"testing"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

func periods(ws []keyprovider.QuotaWindow) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Period)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The widest window is the one a user could escape by rotating their key, so
// it is the one that must end up on the internal user. The rest stay on the
// key. Ordering is by real duration, not by string: "7d" sorts after "30d"
// lexicographically, which would put the wrong allowance on the user.
func TestWidestWindowPicksLongestPeriod(t *testing.T) {
	cases := []struct {
		name   string
		in     []string
		widest string
		rest   []string
	}{
		{"none", nil, "", nil},
		{"single", []string{"24h"}, "24h", nil},
		{"hour and month", []string{"1h", "30d"}, "30d", []string{"1h"}},
		// The real shape on testing: a burst cap, a weekly and a monthly.
		{"three stacked", []string{"1h", "30d", "7d"}, "30d", []string{"1h", "7d"}},
		// 7d vs 30d is the pair that lexicographic ordering gets wrong.
		{"week against month", []string{"30d", "7d"}, "30d", []string{"7d"}},
		{"widest first", []string{"30d", "1h"}, "30d", []string{"1h"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := make([]keyprovider.QuotaWindow, 0, len(tc.in))
			for i, p := range tc.in {
				in = append(in, keyprovider.QuotaWindow{Tokens: int64(i + 1), Period: p})
			}

			widest, rest := WidestWindow(in)
			if widest.Period != tc.widest {
				t.Errorf("widest = %q, want %q", widest.Period, tc.widest)
			}
			if got := periods(rest); !eq(got, tc.rest) {
				t.Errorf("rest = %v, want %v", got, tc.rest)
			}
		})
	}
}

// The split must not lose or duplicate an allowance: every window goes either
// to the user or to the key, exactly once.
func TestWidestWindowKeepsEveryWindow(t *testing.T) {
	in := []keyprovider.QuotaWindow{
		{Tokens: 1000, Period: "1h"},
		{Tokens: 10000, Period: "7d"},
		{Tokens: 1000000, Period: "30d"},
	}

	widest, rest := WidestWindow(in)
	if len(rest)+1 != len(in) {
		t.Fatalf("got %d windows back, want %d", len(rest)+1, len(in))
	}
	if widest.Tokens != 1000000 {
		t.Errorf("widest carries %d tokens, want the 30d allowance", widest.Tokens)
	}
	for _, w := range rest {
		if w.Period == widest.Period {
			t.Errorf("window %q left on the key as well as the user", w.Period)
		}
	}
}
