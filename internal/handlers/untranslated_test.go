package handlers

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// English prose that slipped past the i18n pass renders identically in both
// languages. Two spots did exactly that — the quota note and the word
// "tokens" beside the allowance — so check the rendered German page for
// sentences that stayed English rather than trusting review to catch it.
//
// This looks for known English function words as whole words. It cannot catch
// every miss, but it catches hardcoded prose, which is the realistic failure.
func TestDashboardHasNoUntranslatedProse(t *testing.T) {
	data := dashboardData{
		Lang:   i18n.DE,
		User:   &database.User{Name: "T", Email: "t@example.com"},
		APIKey: &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(24 * time.Hour)},
		// Populate every conditional block, or an untranslated string inside
		// one that stays hidden is not examined at all.
		APIBaseURL:  "https://gw/v1",
		Quotas:      []quotaLine{{Budget: "$0.15", Period: "per day"}},
		ProfileName: "students",
		ExtendUntil: "2026-11-23",
		Models:      []string{"gpt-4o"},
		BudgetUnit:  "$",
		Usage: usageReport{
			Days:  []keyprovider.DailyUsage{{Day: "2026-08-01", Tokens: 10}},
			Total: 10, Peak: 10,
			HasQuota: true, Used: 0.05, Limit: 0.15, QuotaPct: 33,
			// A window is set because production renders the per-window
			// branch; without one the template takes the fallback path and
			// the strings on the branch users actually see go unexamined.
			Windows: []quotaWindowView{{Period: "1h", Label: "pro Stunde",
				Used: 0.05, Limit: 0.15, Pct: 33, ResetsAt: time.Now().Add(time.Hour)}},
			Models: []keyprovider.ModelUsage{{Model: "gpt-4o", Requests: 3,
				PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
		},
		ExpiresInDays: 2,
		ExpiryUrgent:  true,
		NewKey:        "sk-new",
		CSRFToken:     "TOK",
	}

	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Strip attributes and script bodies: they carry English identifiers and
	// copy-button JS that are not user-facing prose.
	html := buf.String()
	html = regexp.MustCompile(`(?s)<script.*?</script>`).ReplaceAllString(html, " ")
	html = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, " ")

	// Whole-word English markers unlikely to appear in German prose.
	markers := []string{"the", "and", "once", "your", "with", "reset", "requests"}
	text := strings.ToLower(html)
	for _, m := range markers {
		if regexp.MustCompile(`\b` + m + `\b`).MatchString(text) {
			t.Errorf("German dashboard contains the English word %q — a string was not translated", m)
		}
	}
}

// A template can reference a catalogue key that does not exist; T then renders
// the key itself, so the page shows "dash.quota.remaining" as literal text.
// This happened while adding the quota display, so check for the pattern
// rather than relying on someone noticing it in a screenshot.
func TestDashboardRendersNoRawCatalogueKeys(t *testing.T) {
	data := dashboardData{
		Lang:   i18n.EN,
		User:   &database.User{Name: "T", Email: "t@example.com"},
		APIKey: &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(24 * time.Hour)},
		// Every conditional block on, so no branch escapes the check.
		APIBaseURL: "https://gw/v1", Quotas: []quotaLine{{Budget: "$0.15", Period: "per day"}},
		ProfileName: "students", ExtendUntil: "2026-11-23",
		Models: []string{"gpt-4o"}, NewKey: "sk-new",
		ExpiresInDays: 2, ExpiryUrgent: true, CSRFToken: "TOK",
		BudgetUnit: "$",
		Usage: usageReport{
			Days:  []keyprovider.DailyUsage{{Day: "2026-08-25", Tokens: 204}},
			Total: 204, Peak: 204,
			HasQuota: true, Used: 0.042, Limit: 0.15, QuotaPct: 28,
			ResetsAt: time.Now().Add(6 * time.Hour),
			// The per-window branch is the one production renders, so the
			// catalogue keys on it have to be examined too.
			Windows: []quotaWindowView{{Period: "1h", Label: "pro Stunde",
				Used: 0.05, Limit: 0.15, Pct: 33, ResetsAt: time.Now().Add(time.Hour)}},
			Models: []keyprovider.ModelUsage{{Model: "gpt-4o", Requests: 3,
				PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
		},
	}

	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Script bodies are checked too, not stripped: the quota reset and expiry
	// badge are built in JS from catalogue strings, so a missing key surfaces
	// there rather than in the markup. Stripping scripts hid exactly that,
	// and nine keys shipped missing before this was caught by hand.
	//
	// Tags are dropped so a src="/static/help.js" is not read as a key.
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(buf.String(), " ")

	// Catalogue keys are dotted lowercase identifiers like "dash.quota.used".
	raw := regexp.MustCompile(`\b(dash|help|badge|admin)\.[a-z][a-z.]*[a-z]\b`)
	if m := raw.FindAllString(text, -1); len(m) > 0 {
		t.Errorf("template rendered raw catalogue keys (missing from messages.go): %v", m)
	}
}
