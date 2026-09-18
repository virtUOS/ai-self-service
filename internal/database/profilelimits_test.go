package database

import (
	"testing"
)

// Limits maps the stored profile onto the provider-neutral limits.
//
// This lives beside the method rather than in the handlers package because
// both the dashboard and the expiry job depend on it: a test next to only one
// caller would leave the other free to drift.
func TestProfileLimits(t *testing.T) {
	tpm, rpm := int64(1000), int64(60)
	p := &Profile{
		Models:   []string{"Qwen/Qwen3.8-27B-FP8"},
		TPMLimit: &tpm,
		RPMLimit: &rpm,
		Quotas: []ProfileQuota{
			{Budget: 0.1, Period: "24h"},
		},
	}
	got := p.Limits()
	if len(got.Models) != 1 || got.Models[0] != "Qwen/Qwen3.8-27B-FP8" {
		t.Errorf("Models = %#v", got.Models)
	}
	if got.TokensPerMinute == nil || *got.TokensPerMinute != 1000 {
		t.Errorf("TokensPerMinute = %v", got.TokensPerMinute)
	}
	// Every field gets pinned by value: a mapping dropped in a refactor would
	// otherwise leave that cap silently unenforced on every live key.
	if got.RequestsPerMinute == nil || *got.RequestsPerMinute != 60 {
		t.Errorf("RequestsPerMinute = %v", got.RequestsPerMinute)
	}
	if len(got.Quotas) != 1 || got.Quotas[0].Budget != 0.1 || got.Quotas[0].Period != "24h" {
		t.Errorf("quotas = %+v, want one window of 0.1/24h", got.Quotas)
	}
}

// A nil profile must produce empty limits rather than panic: an unassigned
// user resolves to no profile, and the dashboard still has to render.
func TestProfileLimitsNil(t *testing.T) {
	var p *Profile
	got := p.Limits()
	if len(got.Quotas) != 0 || got.Models != nil {
		t.Errorf("nil profile gave %#v", got)
	}
}

// Every quota window must survive the mapping. A profile can carry several,
// and dropping one would silently widen the allowance it enforces.
func TestProfileLimitsKeepsEveryQuotaWindow(t *testing.T) {
	p := &Profile{Quotas: []ProfileQuota{
		{Budget: 0.1, Period: "1h"},
		{Budget: 1.0, Period: "24h"},
		{Budget: 5.0, Period: "30d"},
	}}

	got := p.Limits()

	if len(got.Quotas) != 3 {
		t.Fatalf("got %d windows, want 3: %+v", len(got.Quotas), got.Quotas)
	}
	for i, want := range p.Quotas {
		if got.Quotas[i].Budget != want.Budget || got.Quotas[i].Period != want.Period {
			t.Errorf("window %d = %+v, want %+v", i, got.Quotas[i], want)
		}
	}
}
