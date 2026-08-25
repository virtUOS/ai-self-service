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

	got := u.userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"})
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

	if got := usageUI(t, fake).userUsage(context.Background(), nil); got.Total != 0 || len(got.Days) != 0 {
		t.Errorf("no key = %+v, want empty", got)
	}

	failing := keyprovider.NewFake()
	failing.UsageErr = errors.New("gateway down")
	if got := usageUI(t, failing).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}); got.Total != 0 {
		t.Errorf("gateway down = %+v, want empty", got)
	}

	u := &UI{usage: newUsageCache(nil)}
	if got := u.userUsage(context.Background(), &database.APIKey{LiteLLMKey: "sk-live"}); got.Total != 0 {
		t.Errorf("no reporter = %+v, want empty", got)
	}
}

// The peak day scales the bars in the UI; without it every bar is full height.
func TestUserUsageReportsPeak(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{
		"k": {{Day: "2026-08-01", Tokens: 10}, {Day: "2026-08-02", Tokens: 40}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"})
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
