package litellm

import (
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// testProvider is a provider whose client has never refreshed its pricing, so
// conversions use the nominal rate — which is what these assertions expect.
func testProvider() *Provider { return NewProvider(NewClient("http://127.0.0.1:1", "mk")) }

// The adapter owns the token->spend translation; these assertions moved here
// from the handlers when the interface was introduced.
func TestToKeyParamsConvertsQuota(t *testing.T) {
	params := testProvider().toKeyParams(keyprovider.KeyRequest{
		Owner:     "s@uni-osnabrueck.de",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Limits: keyprovider.Limits{
			Quotas: []keyprovider.QuotaWindow{{Tokens: 1_000_000, Period: "24h"}},
		},
	})
	if params.MaxBudget == nil {
		t.Fatal("quota produced no max_budget")
	}
	if diff := *params.MaxBudget - 0.1; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("max_budget = %v, want 0.1 for 1M tokens", *params.MaxBudget)
	}
	if params.BudgetDuration == nil || *params.BudgetDuration != "24h" {
		t.Errorf("budget_duration = %v, want 24h", params.BudgetDuration)
	}
	if params.Metadata["user_email"] != "s@uni-osnabrueck.de" {
		t.Errorf("owner not recorded in metadata: %v", params.Metadata)
	}
}

func TestToKeyParamsNoQuota(t *testing.T) {
	params := testProvider().toKeyParams(keyprovider.KeyRequest{Owner: "a@b.c"})
	if params.MaxBudget != nil || params.BudgetDuration != nil {
		t.Error("no quota should mean no budget fields")
	}
}

// Tokens without a period is not an enforceable window.
func TestToKeyParamsIgnoresIncompleteQuota(t *testing.T) {
	params := testProvider().toKeyParams(keyprovider.KeyRequest{
		Limits: keyprovider.Limits{
			Quotas: []keyprovider.QuotaWindow{{Tokens: 500_000}},
		},
	})
	if params.MaxBudget != nil {
		t.Error("quota without a period produced a budget")
	}
}

// LiteLLM reads an empty model list as "no models"; nil means "all".
func TestToKeyParamsEmptyModelsBecomesNil(t *testing.T) {
	params := testProvider().toKeyParams(keyprovider.KeyRequest{
		Limits: keyprovider.Limits{Models: []string{}},
	})
	if params.Models != nil {
		t.Errorf("Models = %#v, want nil", params.Models)
	}
}

func TestToKeyParamsPassesRateLimits(t *testing.T) {
	tpm, rpm := int64(1000), int64(60)
	params := testProvider().toKeyParams(keyprovider.KeyRequest{
		Limits: keyprovider.Limits{TokensPerMinute: &tpm, RequestsPerMinute: &rpm},
	})
	if params.TPMLimit == nil || *params.TPMLimit != 1000 {
		t.Errorf("TPMLimit = %v", params.TPMLimit)
	}
	if params.RPMLimit == nil || *params.RPMLimit != 60 {
		t.Errorf("RPMLimit = %v", params.RPMLimit)
	}
}
