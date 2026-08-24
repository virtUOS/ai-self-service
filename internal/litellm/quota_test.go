package litellm

import "testing"

// The conversion must be exact at the documented rate: an admin entering
// 1,000,000 tokens must get a 0.10 cap, not 0.099999.
func TestTokensToBudget(t *testing.T) {
	cases := []struct {
		tokens int64
		budget float64
	}{
		{0, 0},
		{1_000_000, 0.1},
		{5_000_000, 0.5},
		{500_000, 0.05},
		{100_000, 0.01},
	}
	for _, c := range cases {
		got := TokensToBudget(c.tokens)
		if diff := got - c.budget; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("TokensToBudget(%d) = %v, want %v", c.tokens, got, c.budget)
		}
	}
}

func TestBudgetToTokensRoundTrip(t *testing.T) {
	for _, tokens := range []int64{1_000, 100_000, 1_000_000, 5_000_000} {
		if got := BudgetToTokens(TokensToBudget(tokens)); got != tokens {
			t.Errorf("round trip of %d gave %d", tokens, got)
		}
	}
}

func TestIsValidQuotaPeriod(t *testing.T) {
	for _, p := range []string{"", "1h", "24h", "7d", "30d"} {
		if !IsValidQuotaPeriod(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	// budget_duration values LiteLLM would reject or that we do not offer.
	for _, p := range []string{"daily", "1d", "12h", "monthly", "junk"} {
		if IsValidQuotaPeriod(p) {
			t.Errorf("%q should be rejected", p)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int64]string{
		0:         "unlimited",
		-1:        "unlimited",
		500:       "500",
		1_000:     "1k",
		1_500:     "1.5k",
		500_000:   "500k",
		1_000_000: "1M",
		1_500_000: "1.5M",
		5_000_000: "5M",
	}
	for in, want := range cases {
		if got := FormatTokens(in); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}
