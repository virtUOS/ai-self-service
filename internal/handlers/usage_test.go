package handlers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/internal/keyprovider"
)

func usageUI(t *testing.T, fake *keyprovider.Fake) *UI {
	t.Helper()
	return &UI{usage: newUsageCache(fake)}
}

// The history is the person's, across every key they have held, so a
// regenerated key shows the same chart as the one it replaced (#116).
func TestUserUsageReportsTheOwnersHistory(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{
		"sub-1": {{Day: "2026-08-01", Tokens: 150}, {Day: "2026-08-03", Tokens: 7}},
	}
	u := usageUI(t, fake)

	for _, ref := range []string{"sk-old", "sk-new"} {
		got := u.userUsage(context.Background(), &database.APIKey{KeyRef: ref}, "sub-1", i18n.EN)
		if got.Total != 157 {
			t.Errorf("%s: Total = %d, want 157", ref, got.Total)
		}
		if len(got.Days) != 2 {
			t.Fatalf("%s: got %d days, want 2", ref, len(got.Days))
		}
		if got.Days[0].Day != "2026-08-01" || got.Days[0].Tokens != 150 {
			t.Errorf("%s: first day = %+v", ref, got.Days[0])
		}
	}
}

// No key, or a provider that cannot report usage, yields an empty report so
// the dashboard omits the card. An unreachable gateway yields no figures too,
// but flags the report as incomplete so the card says why it is empty.
func TestUserUsageWithoutFigures(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{"sub-1": {{Day: "2026-08-01", Tokens: 5}}}

	if got := usageUI(t, fake).userUsage(context.Background(), nil, "sub-1", i18n.EN); got.Total != 0 || len(got.Days) != 0 || got.Incomplete {
		t.Errorf("no key = %+v, want empty and complete", got)
	}

	failing := keyprovider.NewFake()
	failing.UsageErr = errors.New("gateway down")
	got := usageUI(t, failing).userUsage(context.Background(), &database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)
	if got.Total != 0 {
		t.Errorf("gateway down = %+v, want no figures", got)
	}
	if !got.Incomplete {
		t.Error("gateway down: the report is not flagged as incomplete")
	}

	u := &UI{usage: newUsageCache(nil)}
	if got := u.userUsage(context.Background(), &database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN); got.Total != 0 || got.Incomplete {
		t.Errorf("no reporter = %+v, want empty and complete", got)
	}
}

// The peak bar scales the chart; without it every bar is full height. Dates
// are relative to today so the days stay inside the reporting window.
func TestUserUsageReportsPeak(t *testing.T) {
	today := time.Now().UTC()
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{
		"sub-1": {
			{Day: today.AddDate(0, 0, -1).Format("2006-01-02"), Tokens: 10},
			{Day: today.Format("2006-01-02"), Tokens: 40},
		},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{KeyRef: "k"}, "sub-1", i18n.EN)
	if got.Peak != 40 {
		t.Errorf("Peak = %d, want 40", got.Peak)
	}
	if len(got.Bars) != usageWindowDays {
		t.Errorf("drew %d bars, want the whole %d-day window", len(got.Bars), usageWindowDays)
	}
	if last := got.Bars[len(got.Bars)-1]; last.Tokens != 40 {
		t.Errorf("today's bar = %+v, want 40 tokens", last)
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
		Days: []keyprovider.DailyUsage{{Day: "2026-08-01", Tokens: 10}, {Day: "2026-08-03", Tokens: 40}},
		Bars: []usageBar{
			{Label: "01", Title: "2026-08-01", Tokens: 10},
			{Label: "02", Title: "2026-08-02"}, // no traffic: drawn as a gap
			{Label: "03", Title: "2026-08-03", Tokens: 40},
		},
		Total: 50, Peak: 40,
	}
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, withUsage); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, "usage-bar"); n != 3 {
		t.Errorf("rendered %d bars, want one per bucket including the empty one", n)
	}
	if !strings.Contains(out, "height:0%") {
		t.Error("an empty bucket should render as a gap, not a floor-height bar")
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

// Users need to know how much of their allowance is gone, not only what they
// have spent. The figures come from the key's enforced budget so they match
// the limit users actually hit, rather than a 30-day sum that need not align
// with the quota period.
func TestUserUsageReportsPercentOfBudget(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{
		"sub-1": {{Day: "2026-08-25", Tokens: 420_000}},
	}
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"k": {{Period: "24h", Used: 0.042, Limit: 0.15,
			ResetsAt: time.Now().Add(6 * time.Hour)}},
	}

	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{KeyRef: "k"}, "", i18n.EN)
	if len(got.Windows) != 1 {
		t.Fatalf("got %d windows, want the key's budget", len(got.Windows))
	}
	w := got.Windows[0]
	if w.Pct != 28 {
		t.Errorf("Pct = %d, want 28 (0.042 of 0.15)", w.Pct)
	}
	if w.Used != 0.042 || w.Limit != 0.15 {
		t.Errorf("Used/Limit = %v/%v, want 0.042/0.15", w.Used, w.Limit)
	}
}

// An unlimited profile has no allowance to report against, so it must report
// no quota at all rather than one that looks exhausted.
func TestUserUsageUnlimitedReportsNoQuota(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"k": {{Period: "30d", Used: 0.05, Limit: 0}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{KeyRef: "k"}, "", i18n.EN)
	if len(got.Windows) != 0 {
		t.Errorf("unlimited key reported windows %+v", got.Windows)
	}
	if got.Incomplete {
		t.Error("an unlimited key is reported as an incomplete read")
	}
}

// The usage card lists what each model consumed, from the same daily totals as
// the chart, so a researcher can see where their tokens went.
func TestUserUsageReportsModels(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{"sub-1": {{Day: "2026-08-25", Tokens: 10}}}
	fake.ModelUsageByOwner = map[string][]keyprovider.ModelUsage{
		"sub-1": {{Model: "qwen", Requests: 2, PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{KeyRef: "k"}, "sub-1", i18n.EN)
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
				Windows: []quotaWindowView{{Period: "24h", Label: "per day",
					Used: 0.086, Limit: 0.1, Pct: 86, ResetsAt: reset}},
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
