package litellm

import (
	"context"
	"os"
	"testing"
)

// Manual check against a real gateway: do the per-window figures come back,
// and does the arithmetic agree with what the gateway enforces? Skipped unless
// LITELLM_E2E=1, so the normal suite still needs nothing external.
func TestE2EWindowsReportRealUsage(t *testing.T) {
	if os.Getenv("LITELLM_E2E") != "1" {
		t.Skip("set LITELLM_E2E=1 with LITELLM_BASE_URL and LITELLM_MASTER_KEY")
	}
	ref := os.Getenv("LITELLM_E2E_KEY")
	if ref == "" {
		t.Skip("set LITELLM_E2E_KEY to a real key to inspect")
	}

	p := NewProvider(NewClient(os.Getenv("LITELLM_BASE_URL"), os.Getenv("LITELLM_MASTER_KEY")))
	windows, err := p.Windows(context.Background(), ref, os.Getenv("LITELLM_E2E_OWNER"))
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) == 0 {
		t.Fatal("no windows reported for a key that should have them")
	}
	for _, w := range windows {
		t.Logf("%-4s used=%-10d limit=%-10d known=%v resets=%s",
			w.Period, w.UsedTokens, w.LimitTokens, w.UsedKnown, w.ResetsAt.Format("2006-01-02T15:04Z"))
		if w.LimitTokens <= 0 {
			t.Errorf("%s window has no limit", w.Period)
		}
		if w.UsedTokens > w.LimitTokens {
			t.Logf("  note: %s window is over its allowance", w.Period)
		}
	}
}
