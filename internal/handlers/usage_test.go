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

	got := u.userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "", i18n.EN)
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

	if got := usageUI(t, fake).userUsage(context.Background(), nil, "", i18n.EN); got.Total != 0 || len(got.Days) != 0 {
		t.Errorf("no key = %+v, want empty", got)
	}

	failing := keyprovider.NewFake()
	failing.UsageErr = errors.New("gateway down")
	if got := usageUI(t, failing).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "", i18n.EN); got.Total != 0 {
		t.Errorf("gateway down = %+v, want empty", got)
	}

	u := &UI{usage: newUsageCache(nil)}
	if got := u.userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "", i18n.EN); got.Total != 0 {
		t.Errorf("no reporter = %+v, want empty", got)
	}
}

// The peak day scales the bars in the UI; without it every bar is full height.
func TestUserUsageReportsPeak(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"k": {{Day: "2026-08-01", Tokens: 10}, {Day: "2026-08-02", Tokens: 40}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "", i18n.EN)
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
		BudgetUnit: "$",
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
	fake.TotalByRef = map[string]float64{"sk-live": 0.0123}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "", i18n.EN)
	if !got.TotalOnly {
		t.Error("TotalOnly should mark that no per-day breakdown is available")
	}
	if got.TotalSpend != 0.0123 {
		t.Errorf("TotalSpend = %v, want 0.0123 from the key's own spend", got.TotalSpend)
	}
	if len(got.Days) != 0 {
		t.Errorf("no per-day rows expected, got %d", len(got.Days))
	}
}

// When per-day rows exist they are authoritative; the coarse total is not
// fetched or shown as a separate figure.
func TestUserUsagePrefersPerDayRows(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"sk-live": {{Day: "2026-08-01", Tokens: 100}},
	}
	fake.TotalByRef = map[string]float64{"sk-live": 9.99}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}, "", i18n.EN)
	if got.Total != 100 {
		t.Errorf("Total = %d, want 100 from the per-day rows", got.Total)
	}
	if got.TotalOnly {
		t.Error("TotalOnly should be false when a breakdown exists")
	}
}

// Users need to know how much of their allowance is gone, not only what they
// have spent. The figures come from the key's enforced budget so they match
// the limit users actually hit, rather than a 30-day sum that need not align
// with the quota period.
func TestUserUsageReportsPercentOfBudget(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"k": {{Day: "2026-08-25", Tokens: 420_000}},
	}
	fake.QuotaByRef = map[string]keyprovider.Quota{
		"k": {Used: 0.042, Limit: 0.15,
			ResetsAt: time.Now().Add(6 * time.Hour)},
	}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "", i18n.EN)
	if !got.HasQuota {
		t.Fatal("HasQuota should be set when the key has a budget")
	}
	if got.QuotaPct != 28 {
		t.Errorf("QuotaPct = %d, want 28 (0.042 of 0.15)", got.QuotaPct)
	}
	if got.Used != 0.042 || got.Limit != 0.15 {
		t.Errorf("Used/Limit = %v/%v, want 0.042/0.15", got.Used, got.Limit)
	}
}

// An unlimited profile has no allowance to report against, so it must report
// no quota at all rather than one that looks exhausted.
func TestUserUsageUnlimitedReportsNoQuota(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.QuotaByRef = map[string]keyprovider.Quota{
		"k": {Used: 0.05, Limit: 0},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "", i18n.EN)
	if got.HasQuota {
		t.Error("HasQuota should be false for an unlimited key")
	}
	if got.QuotaPct != 0 {
		t.Errorf("unlimited key reported pct=%d", got.QuotaPct)
	}
}

// The usage card lists what each model consumed, from the same log as the
// chart, so a researcher can see where their tokens went.
func TestUserUsageReportsModels(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{"k": {{Day: "2026-08-25", Tokens: 10}}}
	fake.ModelUsageByRef = map[string][]keyprovider.ModelUsage{
		"k": {{Model: "qwen", Requests: 2, PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "", i18n.EN)
	if len(got.Models) != 1 || got.Models[0].Model != "qwen" || got.Models[0].TotalTokens != 10 {
		t.Errorf("Models = %+v, want the qwen row", got.Models)
	}
}

// The gateway logs some requests without a model name. A blank cell reads as
// a rendering fault, so the table says so in the reader's language instead.
func TestDashboardNamesAnUnknownModel(t *testing.T) {
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:       i18n.EN,
		User:       &database.User{Name: "T", Email: "t@example.com"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(24 * time.Hour)},
		CSRFToken:  "TOK",
		BudgetUnit: "$",
		Usage: usageReport{
			Days:   []keyprovider.DailyUsage{{Day: "2026-08-25", Tokens: 10}},
			Total:  10,
			Peak:   10,
			Models: []keyprovider.ModelUsage{{Model: "", Requests: 1, TotalTokens: 10}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "unknown") {
		t.Error("a row with no model name should say so rather than render blank")
	}
	if strings.Contains(out, "<td><code></code></td>") {
		t.Error("a row with no model name rendered an empty code cell")
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
				HasQuota: true, Used: 0.086, Limit: 0.1, QuotaPct: 86,
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
