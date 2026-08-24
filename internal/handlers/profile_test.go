package handlers

import (
	"testing"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
)

// Expiry comes from the profile when set, and from the server otherwise, so
// students and lecturers can have different key lifetimes.
func TestKeyDurationPrefersProfile(t *testing.T) {
	u := &UI{cfg: &config.Config{KeyDurationDays: 90}}

	cases := []struct {
		name    string
		profile *database.Profile
		want    int
	}{
		{"nil profile falls back", nil, 90},
		{"unset profile falls back", &database.Profile{}, 90},
		{"students override", &database.Profile{KeyDurationDays: 30}, 30},
		{"lecturers override", &database.Profile{KeyDurationDays: 365}, 365},
	}
	for _, c := range cases {
		if got := u.keyDuration(c.profile); got != c.want {
			t.Errorf("%s: keyDuration = %d, want %d", c.name, got, c.want)
		}
	}
}

// A token quota must reach LiteLLM as the spend cap it enforces on.
func TestProfileToKeyParamsConvertsQuota(t *testing.T) {
	p := &database.Profile{
		Name:        "students",
		QuotaTokens: 1_000_000,
		QuotaPeriod: "24h",
	}
	params := profileToKeyParams(p, "s@uni-osnabrueck.de")

	if params.MaxBudget == nil {
		t.Fatal("quota did not produce a max_budget")
	}
	if diff := *params.MaxBudget - 0.1; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("max_budget = %v, want 0.1 for 1M tokens", *params.MaxBudget)
	}
	if params.BudgetDuration == nil || *params.BudgetDuration != "24h" {
		t.Errorf("budget_duration = %v, want 24h", params.BudgetDuration)
	}
}

// Without a quota, nothing budget-related should be sent.
func TestProfileToKeyParamsNoQuota(t *testing.T) {
	params := profileToKeyParams(&database.Profile{Name: "unlimited"}, "a@b.c")
	if params.MaxBudget != nil {
		t.Errorf("max_budget = %v, want nil when no quota is configured", *params.MaxBudget)
	}
	if params.BudgetDuration != nil {
		t.Errorf("budget_duration = %v, want nil", *params.BudgetDuration)
	}
}

// A quota with no period is incomplete and must not be sent as a cap.
func TestProfileToKeyParamsIgnoresIncompleteQuota(t *testing.T) {
	params := profileToKeyParams(&database.Profile{QuotaTokens: 500_000}, "a@b.c")
	if params.MaxBudget != nil {
		t.Error("quota without a period produced a budget")
	}
}

// An empty model list means "all models"; LiteLLM treats [] as "none".
func TestProfileToKeyParamsEmptyModels(t *testing.T) {
	params := profileToKeyParams(&database.Profile{Models: []string{}}, "a@b.c")
	if params.Models != nil {
		t.Errorf("Models = %#v, want nil so LiteLLM allows all", params.Models)
	}
}

func TestDashboardQuotaRendering(t *testing.T) {
	cases := []struct {
		profile *database.Profile
		tokens  string
		period  string
	}{
		{nil, "", ""},
		{&database.Profile{}, "", ""},
		{&database.Profile{QuotaTokens: 1_500_000, QuotaPeriod: "24h"}, "1.5M", "per day"},
		{&database.Profile{QuotaTokens: 500_000, QuotaPeriod: "7d"}, "500k", "per week"},
		{&database.Profile{QuotaTokens: 5_000_000, QuotaPeriod: "30d"}, "5M", "per month"},
		// tokens without a period is not an enforceable quota
		{&database.Profile{QuotaTokens: 1000}, "", ""},
	}
	for _, c := range cases {
		if got := profileQuota(c.profile); got != c.tokens {
			t.Errorf("profileQuota(%v) = %q, want %q", c.profile, got, c.tokens)
		}
		if got := profilePeriod(c.profile); got != c.period {
			t.Errorf("profilePeriod(%v) = %q, want %q", c.profile, got, c.period)
		}
	}
}
