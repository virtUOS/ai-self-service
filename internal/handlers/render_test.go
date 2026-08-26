package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
)

// Templates are parsed at construction time in production; these tests catch
// field/name drift between the handler structs and the HTML.
func TestDashboardTemplateRenders(t *testing.T) {
	tmpl := parseDashboardTemplate()
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, dashboardData{
		Lang:       i18n.DE,
		User:       &database.User{Name: "Test", Email: "t@example.com"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-abc123", ExpiresAt: time.Now().Add(24 * time.Hour)},
		NewKey:     "sk-brand-new",
		APIBaseURL: "https://litellm.example.com/v1",
		CSRFToken:  "TOK123",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// language switcher + logout + extend + regenerate + delete.
	if n := strings.Count(out, `name="csrf_token" value="TOK123"`); n != 5 {
		t.Errorf("csrf fields rendered = %d, want 5", n)
	}
	if !strings.Contains(out, "sk-brand-new") {
		t.Error("new key not rendered")
	}
	// The API base must be the gateway, not the portal: a client pointed at the
	// portal gets 404s because it serves HTML, not the OpenAI API.
	if !strings.Contains(out, "https://litellm.example.com/v1") {
		t.Error("dashboard does not show the gateway API base URL")
	}
	// Every form must carry a token — none may post unprotected.
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields — a form would post unprotected", forms, toks)
	}
}

// The no-key branch renders a different form set; it must be covered too.
func TestDashboardTemplateNoKeyBranch(t *testing.T) {
	tmpl := parseDashboardTemplate()
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, dashboardData{
		Lang:      i18n.DE,
		User:      &database.User{Name: "Test", Email: "t@example.com"},
		CSRFToken: "TOK789",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// language switcher + logout + generate
	if n := strings.Count(out, `name="csrf_token" value="TOK789"`); n != 3 {
		t.Errorf("csrf fields rendered = %d, want 3", n)
	}
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields", forms, toks)
	}
}

func TestAdminTemplateRenders(t *testing.T) {
	tmpl := parseAdminTemplate()
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, adminData{
		Profiles: []database.Profile{
			{ID: 1, Name: "default", IsDefault: true},
			{ID: 2, Name: "students", KeyDurationDays: 30, Quotas: []database.ProfileQuota{
				{Tokens: 100_000, Period: "24h"},
				{Tokens: 1_500_000, Period: "30d"},
			}},
		},
		Users:     []userRow{{User: database.User{ID: 2, Name: "U", Email: "u@x.de"}}},
		CSRFToken: "TOK456",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// language switcher + create form + user profile form + one delete form
	// (the default profile hides its own delete)
	if n := strings.Count(out, `name="csrf_token" value="TOK456"`); n != 4 {
		t.Errorf("csrf fields rendered = %d, want 4", n)
	}
	// Quotas must render in tokens, formatted, not as raw spend.
	// Several windows must all appear, not just the first.
	if !strings.Contains(out, "100k per day") || !strings.Contains(out, "1.5M per month") {
		t.Error("stacked quota windows not rendered in admin table")
	}
	if !strings.Contains(out, "30 days") {
		t.Error("per-profile expiry not rendered in admin table")
	}
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields", forms, toks)
	}
}

// Tooltips explain jargon like TPM to admins who do not know it. Assert they
// render with real text, since Go's contextual escaping would mangle a badly
// quoted attribute rather than fail loudly.
func TestAdminTooltipsRender(t *testing.T) {
	tmpl := parseAdminTemplate()
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, adminData{
		Lang:      i18n.DE,
		Profiles:  []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		CSRFToken: "T",
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if n := strings.Count(out, `class="help"`); n < 10 {
		t.Errorf("only %d tooltips rendered, want the full set", n)
	}
	for _, want := range []string{"Token pro Minute", "Anfragen pro Minute", "UTC"} {
		if !strings.Contains(out, want) {
			t.Errorf("tooltip text %q missing", want)
		}
	}
	// A mangled attribute shows up as escaped quotes inside data-help.
	if strings.Contains(out, `data-help="ZgotmplZ`) {
		t.Error("tooltip attribute was neutered by contextual escaping")
	}
}

func TestDashboardTooltipsRender(t *testing.T) {
	tmpl := parseDashboardTemplate()
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, dashboardData{
		Lang:       i18n.DE,
		User:       &database.User{Name: "T", Email: "t@x.de"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(48 * time.Hour)},
		APIBaseURL: "https://litellm.example.com/v1",
		Quotas:     []quotaLine{{Tokens: "1.5M", Period: "per day"}}, ProfileName: "students",
		CSRFToken: "T",
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if n := strings.Count(out, `class="help"`); n < 4 {
		t.Errorf("only %d tooltips rendered on the dashboard", n)
	}
}

// The model picker must list exactly what the gateway serves, so an admin
// cannot type a model that does not exist.
func TestAdminModelPickerRenders(t *testing.T) {
	tmpl := parseAdminTemplate()
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, adminData{
		AvailableModels: []string{"Qwen/Qwen3.8-27B-FP8", "bge-m3"},
		Profiles:        []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		CSRFToken:       "T",
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, m := range []string{"Qwen/Qwen3.8-27B-FP8", "bge-m3"} {
		if !strings.Contains(out, `value="`+m+`"`) {
			t.Errorf("model %q not offered as a checkbox", m)
		}
	}
	if strings.Contains(out, `name="models"`) {
		t.Error("free-text fallback rendered alongside the picker")
	}
}

// With no list available the form must still be usable.
func TestAdminModelPickerFallsBackToText(t *testing.T) {
	tmpl := parseAdminTemplate()
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, adminData{
		Profiles:  []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		CSRFToken: "T",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `name="models"`) {
		t.Error("no free-text fallback when the gateway list is unavailable")
	}
}

// An empty limit must say what actually applies. A bare dash leaves the admin
// guessing whether it means unlimited, zero, or not-yet-configured.
func TestAdminTableStatesDefaults(t *testing.T) {
	tmpl := parseAdminTemplate()
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, adminData{
		DefaultKeyDays: 90,
		Profiles:       []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		CSRFToken:      "T",
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"all models",               // no model restriction
		"unlimited",                // no TPM/RPM/quota
		"90 days (server default)", // expiry falls back to the server value
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table does not state %q", want)
		}
	}
	if strings.Contains(out, "&mdash;") {
		t.Error("table still renders bare dashes for empty values")
	}
}

func TestFormatPeriod(t *testing.T) {
	for in, want := range map[string]string{
		"1h": "per hour", "24h": "per day", "7d": "per week", "30d": "per month",
		"": "", "weird": "weird",
	} {
		if got := formatPeriod(in); got != want {
			t.Errorf("formatPeriod(%q) = %q, want %q", in, got, want)
		}
	}
}

// Nothing user-facing may remain in English on a German page. This catches
// strings that were added later and never wired to the catalogue.
func TestAdminPageFullyGerman(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Lang:            i18n.DE,
		Langs:           i18n.Supported,
		DefaultKeyDays:  90,
		AvailableModels: []string{"Qwen/Qwen3.8-27B-FP8"},
		Profiles:        []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		Users:           []userRow{{User: database.User{ID: 2, Name: "U", Email: "u@x.de"}}},
		CSRFToken:       "T",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Phrases that only appear if a string was missed.
	for _, english := range []string{
		"Requests fail once the limit is hit",
		"Tokens per minute",
		"Requests per minute",
		">Users<",
		">Profiles<",
		">Audit log<",
		"Allowed models",
		"Usage limit (tokens)",
	} {
		if strings.Contains(out, english) {
			t.Errorf("untranslated on the German page: %q", english)
		}
	}
	// And confirm the German actually rendered.
	for _, german := range []string{"Benutzende", "Audit-Log", "Token pro Minute"} {
		if !strings.Contains(out, german) {
			t.Errorf("expected German text %q missing", german)
		}
	}
}

func TestDashboardPageFullyGerman(t *testing.T) {
	var buf bytes.Buffer
	err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:  i18n.DE,
		Langs: i18n.Supported,
		User:  &database.User{Name: "T", Email: "t@x.de"},
		APIKey: &database.APIKey{
			KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
		APIBaseURL: "https://litellm.example.com/v1",
		CSRFToken:  "T",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, english := range []string{
		"Your account", "API key</h2>", "Using your key", "Base URL</dt>",
	} {
		if strings.Contains(out, english) {
			t.Errorf("untranslated on the German dashboard: %q", english)
		}
	}
	if !strings.Contains(out, "Ihr Konto") {
		t.Error("German dashboard did not render")
	}
}
