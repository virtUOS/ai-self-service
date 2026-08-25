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
		QuotaTokens: "1.5M",
		QuotaPeriod: "24h",
		ProfileName: "students",
		ExtendUntil: "2026-11-23",
		Models:      []string{"gpt-4o"},
		Usage: usageReport{
			Days:  []keyprovider.DailyUsage{{Day: "2026-08-01", Tokens: 10}},
			Total: 10, Peak: 10,
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
		APIBaseURL: "https://gw/v1", QuotaTokens: "1.5M", QuotaPeriod: "24h",
		ProfileName: "students", ExtendUntil: "2026-11-23",
		Models: []string{"gpt-4o"}, NewKey: "sk-new",
		ExpiresInDays: 2, ExpiryUrgent: true, CSRFToken: "TOK",
		Usage: usageReport{
			Days:  []keyprovider.DailyUsage{{Day: "2026-08-25", Tokens: 204}},
			Total: 204, Peak: 204,
			HasQuota: true, Used: 420_000, Remaining: 1_080_000, QuotaPct: 28,
			ResetsAt: time.Now().Add(6 * time.Hour),
		},
	}

	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Strip tags first: a src="/static/help.js" is not a rendered key. What
	// remains is the text a user actually sees.
	text := regexp.MustCompile(`(?s)<script.*?</script>`).ReplaceAllString(buf.String(), " ")
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, " ")

	// Catalogue keys are dotted lowercase identifiers like "dash.quota.used".
	raw := regexp.MustCompile(`\b(dash|help|badge|admin)\.[a-z][a-z.]*[a-z]\b`)
	if m := raw.FindAllString(text, -1); len(m) > 0 {
		t.Errorf("template rendered raw catalogue keys (missing from messages.go): %v", m)
	}
}
