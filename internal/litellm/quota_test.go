package litellm

import "testing"

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
