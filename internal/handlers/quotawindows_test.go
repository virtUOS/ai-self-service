package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

func windowsUI(fake *keyprovider.Fake) *UI {
	return &UI{usage: newUsageCache(fake), keys: fake}
}

// A profile can hold several allowances at once, and the card shows a bar per
// window. Reporting only one would misstate what the user has left: room on a
// monthly allowance says nothing about an hourly cap that is nearly spent.
func TestUserUsageReportsEveryWindow(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {
			{Period: "1h", Used: 0.8, Limit: 1.0, UsedKnown: true},
			{Period: "30d", Used: 0.00959, Limit: 1.0, UsedKnown: true},
		},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{LiteLLMKey: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 2 {
		t.Fatalf("got %d windows, want 2", len(got.Windows))
	}
	if got.Windows[0].Pct != 80 {
		t.Errorf("1h window at %d%%, want 80", got.Windows[0].Pct)
	}
	if got.Windows[0].Label == "" {
		t.Error("window is missing its formatted label")
	}
}

// The headline figure must name the window that binds — the one that will
// reject the next request — not the widest. Reporting the monthly window's
// ample headroom while an hourly cap is nearly spent tells the user they have
// room they do not have.
func TestUserUsageHeadlineFollowsTheBindingWindow(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {
			{Period: "1h", Used: 0.00095, Limit: 0.001, UsedKnown: true},
			{Period: "30d", Used: 0.00959, Limit: 1.0, UsedKnown: true},
		},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{LiteLLMKey: "sk-live"}, "sub-1", i18n.EN)

	if got.QuotaPct != 95 {
		t.Errorf("headline at %d%%, want the 95%% hourly window", got.QuotaPct)
	}
}

// Spend logging can be switched off, and consumption per window is summed from
// it. A bar drawn from a silent zero would promise a full allowance, so the
// card falls back to the single gateway-reported bar instead.
func TestUserUsageFallsBackWhenWindowSpendUnknown(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {
			{Period: "1h", Limit: 0.001, UsedKnown: false},
			{Period: "30d", Used: 0.00959, Limit: 1.0, UsedKnown: true},
		},
	}
	fake.QuotaByRef = map[string]keyprovider.Quota{
		"sk-live": {Used: 0.00959, Limit: 1.0},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{LiteLLMKey: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 0 {
		t.Errorf("got %d windows, want none when a window's usage is unknown", len(got.Windows))
	}
	if !got.HasQuota {
		t.Error("fell back to no quota at all rather than the single bar")
	}
	if got.Used != 0.00959 {
		t.Errorf("fallback bar shows %v used, want the gateway figure", got.Used)
	}
}

// A gateway that cannot report windows must not break the card.
func TestUserUsageSurvivesWindowError(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsErr = errors.New("gateway down")
	fake.QuotaByRef = map[string]keyprovider.Quota{
		"sk-live": {Used: 0.01, Limit: 0.1},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{LiteLLMKey: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 0 {
		t.Error("windows reported despite the error")
	}
	if !got.HasQuota || got.QuotaPct != 10 {
		t.Errorf("single bar not preserved: %+v", got)
	}
}

// An over-spent window renders as full rather than overflowing its bar.
func TestQuotaPctClampsAtFull(t *testing.T) {
	if got := quotaPct(2.0, 1.0); got != 100 {
		t.Errorf("quotaPct(2,1) = %d, want 100", got)
	}
	if got := quotaPct(10, 0); got != 0 {
		t.Errorf("quotaPct with no limit = %d, want 0", got)
	}
}

// The binding window is the one closest to exhaustion, whatever its period.
func TestBindingWindowPicksTightestHeadroom(t *testing.T) {
	windows := []quotaWindowView{
		{Period: "1h", Pct: 20},
		{Period: "7d", Pct: 88},
		{Period: "30d", Pct: 1},
	}
	if got := bindingWindow(windows); got == nil || got.Period != "7d" {
		t.Errorf("binding window = %+v, want the 7d one at 88%%", got)
	}
	if bindingWindow(nil) != nil {
		t.Error("binding window reported for an empty set")
	}
}

// Windows come back tightest first so the card reads in the order a user hits
// them, and the reset time is carried through for each.
func TestUserUsageKeepsWindowResetTimes(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).UTC()
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {{Period: "1h", Used: 0.1, Limit: 1.0, ResetsAt: reset, UsedKnown: true}},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{LiteLLMKey: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 1 || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("reset time not carried through: %+v", got.Windows)
	}
}
