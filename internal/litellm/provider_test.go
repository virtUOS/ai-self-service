package litellm

import (
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// testProvider is a provider pointed at nothing: toKeyParams never calls out.
func testProvider() *Provider { return NewProvider(NewClient("http://127.0.0.1:1", "mk")) }

// The adapter passes the budget straight through; these assertions moved here
// from the handlers when the interface was introduced.
func TestToKeyParamsPassesQuotaThrough(t *testing.T) {
	params := testProvider().toKeyParams(keyprovider.KeyRequest{
		Owner:     "s@uni-osnabrueck.de",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Limits: keyprovider.Limits{
			Quotas: []keyprovider.QuotaWindow{{Budget: 0.1, Period: "24h"}},
		},
	})
	if params.MaxBudget == nil {
		t.Fatal("quota produced no max_budget")
	}
	if diff := *params.MaxBudget - 0.1; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("max_budget = %v, want 0.1", *params.MaxBudget)
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

// A budget without a period is not an enforceable window.
func TestToKeyParamsIgnoresIncompleteQuota(t *testing.T) {
	params := testProvider().toKeyParams(keyprovider.KeyRequest{
		Limits: keyprovider.Limits{
			Quotas: []keyprovider.QuotaWindow{{Budget: 0.05}},
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
