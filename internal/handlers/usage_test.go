package handlers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

func usageUI(t *testing.T, fake *keyprovider.Fake) *UI {
	t.Helper()
	return &UI{usage: newUsageCache(fake)}
}

// Usage is reported for the key the user holds now. Regenerating starts the
// history over, because a new key is a different key upstream. See issue #9.
func TestUserUsageReportsCurrentKey(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"sk-live": {{Day: "2026-08-01", Tokens: 150}, {Day: "2026-08-03", Tokens: 7}},
	}
	u := usageUI(t, fake)

	got := u.userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "")
	if got.Total != 157 {
		t.Errorf("Total = %d, want 157", got.Total)
	}
	if len(got.Days) != 2 {
		t.Fatalf("got %d days, want 2", len(got.Days))
	}
	if got.Days[0].Day != "2026-08-01" || got.Days[0].Tokens != 150 {
		t.Errorf("first day = %+v", got.Days[0])
	}
}

// No key, an unreachable gateway, or a provider that cannot report usage must
// all yield an empty report so the dashboard omits the card.
func TestUserUsageDegradesQuietly(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{"sk-live": {{Day: "2026-08-01", Tokens: 5}}}

	if got := usageUI(t, fake).userUsage(context.Background(), nil, ""); got.Total != 0 || len(got.Days) != 0 {
		t.Errorf("no key = %+v, want empty", got)
	}

	failing := keyprovider.NewFake()
	failing.UsageErr = errors.New("gateway down")
	if got := usageUI(t, failing).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, ""); got.Total != 0 {
		t.Errorf("gateway down = %+v, want empty", got)
	}

	u := &UI{usage: newUsageCache(nil)}
	if got := u.userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, ""); got.Total != 0 {
		t.Errorf("no reporter = %+v, want empty", got)
	}
}

// The peak day scales the bars in the UI; without it every bar is full height.
func TestUserUsageReportsPeak(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"k": {{Day: "2026-08-01", Tokens: 10}, {Day: "2026-08-02", Tokens: 40}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "")
	if got.Peak != 40 {
		t.Errorf("Peak = %d, want 40", got.Peak)
	}
}

// The chart must render a bar per day and scale to the peak, and must say so
// plainly when there is no usage rather than drawing an empty chart.
func TestDashboardRendersUsage(t *testing.T) {
	base := dashboardData{
		Lang:       i18n.DE,
		User:       &database.User{Name: "T", Email: "t@example.com"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(24 * time.Hour)},
		APIBaseURL: "https://gw/v1",
		CSRFToken:  "TOK",
	}

	withUsage := base
	withUsage.Usage = usageReport{
		Days:  []keyprovider.DailyUsage{{Day: "2026-08-01", Tokens: 10}, {Day: "2026-08-02", Tokens: 40}},
		Total: 50, Peak: 40,
	}
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, withUsage); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, "usage-bar"); n != 2 {
		t.Errorf("rendered %d bars, want 2", n)
	}
	// The peak day is full height; the quieter day is scaled below it.
	if !strings.Contains(out, "height:100%") {
		t.Error("peak day should be full height")
	}
	if !strings.Contains(out, "height:25%") {
		t.Error("10 of 40 tokens should render at 25%")
	}
	if !strings.Contains(out, "50") {
		t.Error("total not rendered")
	}

	buf.Reset()
	if err := parseDashboardTemplate().Execute(&buf, base); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(buf.String(), "usage-bar") {
		t.Error("no usage should render no bars")
	}
	if !strings.Contains(buf.String(), "noch kein Verbrauch") {
		t.Error("empty state message not rendered")
	}
}

// Per-request spend logging can be off upstream — it is on this deployment,
// to bound a LiteLLM memory leak. The key's own cumulative spend still works,
// so the card must report a real total rather than claiming no usage.
func TestUserUsageFallsBackToTotal(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = nil // logging disabled: no per-day rows
	fake.TotalByRef = map[string]int64{"sk-live": 545}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "")
	if got.Total != 545 {
		t.Errorf("Total = %d, want 545 from the key's own spend", got.Total)
	}
	if len(got.Days) != 0 {
		t.Errorf("no per-day rows expected, got %d", len(got.Days))
	}
	if !got.TotalOnly {
		t.Error("TotalOnly should mark that no per-day breakdown is available")
	}
}

// When per-day rows exist they are authoritative; the coarse total is not
// fetched or shown as a separate figure.
func TestUserUsagePrefersPerDayRows(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"sk-live": {{Day: "2026-08-01", Tokens: 100}},
	}
	fake.TotalByRef = map[string]int64{"sk-live": 999999}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "")
	if got.Total != 100 {
		t.Errorf("Total = %d, want 100 from the per-day rows", got.Total)
	}
	if got.TotalOnly {
		t.Error("TotalOnly should be false when a breakdown exists")
	}
}

// Users need to know what is left, not only what they have spent. The figures
// come from the key's enforced budget so they match the limit users actually
// hit, rather than a 30-day sum that need not align with the quota period.
func TestUserUsageReportsRemaining(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"k": {{Day: "2026-08-25", Tokens: 420_000}},
	}
	fake.QuotaByRef = map[string]keyprovider.Quota{
		"k": {UsedTokens: 420_000, LimitTokens: 1_500_000,
			ResetsAt: time.Now().Add(6 * time.Hour)},
	}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "")
	if !got.HasQuota {
		t.Fatal("HasQuota should be set when the key has a budget")
	}
	if got.Remaining != 1_080_000 {
		t.Errorf("Remaining = %d, want 1080000", got.Remaining)
	}
	if got.QuotaPct != 28 {
		t.Errorf("QuotaPct = %d, want 28 (420k of 1.5M)", got.QuotaPct)
	}
}

// An unlimited profile has nothing remaining to report, and must not be shown
// as though its quota were exhausted.
func TestUserUsageUnlimitedHasNoRemaining(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.QuotaByRef = map[string]keyprovider.Quota{
		"k": {UsedTokens: 545, LimitTokens: 0},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "")
	if got.HasQuota {
		t.Error("HasQuota should be false for an unlimited key")
	}
	if got.Remaining != 0 || got.QuotaPct != 0 {
		t.Errorf("unlimited key reported remaining=%d pct=%d", got.Remaining, got.QuotaPct)
	}
}

// The reset time is rendered relative in the browser, so the page must carry
// the raw timestamp plus translated units. A UTC wall-clock alone forces the
// user to do timezone arithmetic to answer "when can I work again".
func TestQuotaResetIsRenderedRelative(t *testing.T) {
	reset := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	for _, lang := range []i18n.Lang{i18n.DE, i18n.EN} {
		var buf bytes.Buffer
		if err := parseDashboardTemplate().Execute(&buf, dashboardData{
			Lang:      lang,
			User:      &database.User{Name: "T", Email: "t@example.com"},
			APIKey:    &database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(24 * time.Hour)},
			CSRFToken: "TOK",
			Usage: usageReport{
				HasQuota: true, Used: 8_622, Remaining: 1_378, QuotaPct: 86,
				ResetsAt: reset,
			},
		}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		// The element carries the instant; the browser formats it.
		if !strings.Contains(out, `data-reset="2026-08-25T10:00:00Z"`) {
			t.Errorf("%s: reset timestamp not exposed for client-side rendering", lang)
		}
		// A bare UTC wall clock must not be the only thing shown.
		if strings.Contains(out, "10:00 UTC") {
			t.Errorf("%s: still renders a raw UTC time", lang)
		}
	}
}
