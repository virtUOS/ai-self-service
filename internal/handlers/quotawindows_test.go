package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/internal/keyprovider"
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
			{Period: "1h", Used: 0.8, Limit: 1.0},
			{Period: "30d", Used: 0.00959, Limit: 1.0},
		},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

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

// A gateway that cannot report windows must not break the card, and the card
// must say so rather than read as "no limit".
func TestUserUsageSurvivesWindowError(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsErr = errors.New("gateway down")

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 0 {
		t.Error("windows reported despite the error")
	}
	if !got.Incomplete {
		t.Error("a failed read is not reported as incomplete")
	}
	if got.KeyMissing {
		t.Error("a failed read is reported as a missing key")
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

// Windows come back tightest first so the card reads in the order a user hits
// them, and the reset time is carried through for each.
func TestUserUsageKeepsWindowResetTimes(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).UTC()
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {{Period: "1h", Used: 0.1, Limit: 1.0, ResetsAt: reset}},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 1 || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("reset time not carried through: %+v", got.Windows)
	}
}
