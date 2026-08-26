package handlers

import (
	"bytes"
	"html"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/web"
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

// The edit button seeds the form with the profile's current values. Passing
// them through printf %q put literal quote characters inside the value, so
// saving wrote them back: "test quota" became "\"test quota\"" and grew a
// pair of quotes on every edit.
//
// html/template quotes the JS string itself, so the value must arrive bare.
func TestAdminEditButtonPassesBareValues(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Profiles:  []database.Profile{{ID: 2, Name: "test quota", Description: "a desc"}},
		Users:     []userRow{},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Decode the attribute the way a browser would, then check the argument
	// list holds the bare name rather than one wrapped in quote characters.
	out := html.UnescapeString(buf.String())
	if strings.Contains(out, `openEditProfile(2, "\"test quota\""`) ||
		strings.Contains(out, `, "\"test quota\""`) {
		t.Error("profile name carries literal quote characters")
	}
	if !strings.Contains(out, `"test quota"`) {
		t.Error("profile name not passed to the edit button")
	}
}

// The profiles table has more columns than fit a narrow viewport, and quota
// windows stack inside their own cell. It must be allowed to overflow and
// scroll: compressing it clipped the quota column off the right edge, so an
// admin could not see the windows they had just saved.
func TestAdminTableCanScroll(t *testing.T) {
	css, err := fs.ReadFile(web.StaticFS, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)

	if !strings.Contains(style, ".table-wrap { overflow-x: auto; }") {
		t.Error("table wrapper does not scroll horizontally")
	}
	// Without a min-width the table compresses to the wrapper instead of
	// overflowing it, so the scrollbar never appears and columns are clipped.
	if !strings.Contains(style, "min-width: max-content") {
		t.Error("table has no min-width, so it compresses rather than scrolling")
	}
}

// A profile's quota windows must reach both places the admin panel shows them:
// the table cell, and the edit button's argument list that populates the form.
// They were absent from both because the query behind this page did not load
// the relation, so a profile with two windows rendered as "unlimited" and its
// edit form opened empty.
func TestAdminShowsProfileQuotaWindows(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Profiles: []database.Profile{{
			ID: 2, Name: "test quota",
			Quotas: []database.ProfileQuota{
				{Tokens: 1_000, Period: "1h"},
				{Tokens: 1_000_000, Period: "30d"},
			},
		}},
		Users:     []userRow{},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := html.UnescapeString(buf.String())

	// The table cell renders each window in the admin's own units.
	for _, want := range []string{"1k", "1M"} {
		if !strings.Contains(out, want) {
			t.Errorf("table does not show the %s window", want)
		}
	}
	// The edit button carries them as JSON so the form opens populated. The
	// field names are Go's, which is what setQuotaRows reads.
	for _, want := range []string{`"Period":"1h"`, `"Period":"30d"`} {
		if !strings.Contains(out, want) {
			t.Errorf("edit button does not carry %s", want)
		}
	}
	// A profile with windows must not advertise itself as unlimited. The
	// default language is German, so that is the string to look for.
	if strings.Contains(out, ">unbegrenzt</em>") {
		t.Error("a profile with two windows still renders as unlimited")
	}
}

// The usage card draws a bar per allowance window. A single bar for the widest
// window would show headroom the user does not have when a tighter cap is
// nearly spent.
func TestDashboardDrawsABarPerWindow(t *testing.T) {
	var buf bytes.Buffer
	err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:   i18n.EN,
		Langs:  i18n.Supported,
		User:   &database.User{Email: "a@b.c"},
		APIKey: &database.APIKey{KeyPrefix: "sk-x", ExpiresAt: time.Now().Add(24 * time.Hour)},
		Usage: usageReport{
			HasQuota: true,
			Windows: []quotaWindowView{
				{Period: "1h", Label: "per hour", LimitText: "1k", Used: 950, Limit: 1_000, Remaining: 50, Pct: 95},
				{Period: "30d", Label: "per month", LimitText: "1M", Used: 9_590, Limit: 1_000_000, Remaining: 990_410, Pct: 1},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if got := strings.Count(out, "quota-fill"); got != 2 {
		t.Errorf("drew %d bars, want one per window", got)
	}
	// The nearly-spent window is marked as full so it reads as urgent.
	if !strings.Contains(out, "quota-full") {
		t.Error("the 95% window is not marked as nearly exhausted")
	}
	for _, want := range []string{"per hour", "per month", "width:95%", "width:1%"} {
		if !strings.Contains(out, want) {
			t.Errorf("card is missing %q", want)
		}
	}
}

// With no per-window figures the card keeps the single bar rather than showing
// nothing, so a gateway without spend logging still reports a quota.
func TestDashboardFallsBackToOneBar(t *testing.T) {
	var buf bytes.Buffer
	err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:   i18n.EN,
		Langs:  i18n.Supported,
		User:   &database.User{Email: "a@b.c"},
		APIKey: &database.APIKey{KeyPrefix: "sk-x", ExpiresAt: time.Now().Add(24 * time.Hour)},
		Usage: usageReport{
			HasQuota: true, Used: 9_590, Remaining: 990_410, QuotaPct: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(buf.String(), "quota-fill"); got != 1 {
		t.Errorf("drew %d bars, want exactly one in the fallback", got)
	}
}

// The admin panel shows each user's OIDC subject, because that is the value
// ADMIN_IDS should list: an email address can be reassigned by the IdP, so an
// allowlist keyed on it grants rights to whoever holds the address today.
// Without this an operator would have to query the database to find it.
func TestAdminShowsOIDCSubject(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Profiles: []database.Profile{},
		Users: []userRow{{
			User: database.User{ID: 1, Name: "R", Email: "r@uni-osnabrueck.de", OIDCSub: "7ca16f0b-d201"},
		}},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "7ca16f0b-d201") {
		t.Error("admin panel does not show the OIDC subject")
	}
}
