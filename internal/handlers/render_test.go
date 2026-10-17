package handlers

import (
	"bytes"
	"html"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/web"
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

// A portal being retired tells users where to go instead. Its keys are not
// revoked; they run out on their own, so the banner names no date and makes
// no threat. Nothing of the sort appears otherwise.
func TestDashboardShowsSuccessorBanner(t *testing.T) {
	base := dashboardData{
		Lang:      i18n.EN,
		User:      &database.User{Name: "T", Email: "t@example.com"},
		CSRFToken: "TOK",
	}
	render := func(d dashboardData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := parseDashboardTemplate().Execute(&buf, d); err != nil {
			t.Fatal(err)
		}
		return html.UnescapeString(buf.String())
	}

	out := render(base)
	if strings.Contains(out, "being retired") {
		t.Error("banner shown with no successor configured")
	}

	with := base
	with.SuccessorURL = "https://ai-keys.example.edu"
	out = render(with)
	for _, want := range []string{
		`href="https://ai-keys.example.edu"`, "being retired", "create your key on the new portal",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner is missing %q", want)
		}
	}
	if strings.Contains(out, "revoked") {
		t.Error("banner threatens revocation; keys expire on their own")
	}
}

// A retired portal offers no way to create or prolong a key: the successor
// does that now. Deleting stays so users can clean up before the cut-off.
func TestRetiredPortalHidesGenerateAndExtend(t *testing.T) {
	render := func(d dashboardData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := parseDashboardTemplate().Execute(&buf, d); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	base := dashboardData{
		Lang:        i18n.EN,
		User:        &database.User{Name: "T", Email: "t@example.com"},
		CSRFToken:   "TOK",
		ExtendUntil: "2026-12-01",
	}

	withKey := base
	withKey.APIKey = &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(24 * time.Hour)}
	if out := render(withKey); !strings.Contains(out, `action="/key/extend"`) || !strings.Contains(out, `action="/key/generate"`) {
		t.Fatal("open portal is missing the extend or regenerate form")
	}

	withKey.SuccessorURL = "https://ai-keys.example.edu"
	out := render(withKey)
	for _, gone := range []string{`action="/key/extend"`, `action="/key/generate"`} {
		if strings.Contains(out, gone) {
			t.Errorf("retired portal still offers %s", gone)
		}
	}
	if !strings.Contains(out, `action="/key/delete"`) {
		t.Error("retired portal lost the delete form")
	}

	noKey := base
	noKey.SuccessorURL = "https://ai-keys.example.edu"
	if out := render(noKey); strings.Contains(out, `action="/key/generate"`) {
		t.Error("retired portal offers to generate a key to a user without one")
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
				{Budget: 0.01, Period: "24h"},
				{Budget: 0.15, Period: "30d"},
			}},
		},
		Users:      []userRow{{User: database.User{ID: 2, Name: "U", Email: "u@x.de"}}},
		CSRFToken:  "TOK456",
		BudgetUnit: "$",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// language switcher + create form + user profile form + one delete form
	// (the default profile hides its own delete) + the Admins tab's grant form
	if n := strings.Count(out, `name="csrf_token" value="TOK456"`); n != 5 {
		t.Errorf("csrf fields rendered = %d, want 5", n)
	}
	// Quotas must render as spend in the deployment's own unit.
	// Several windows must all appear, not just the first.
	out = html.UnescapeString(out)
	if !strings.Contains(out, "$0.01 per day") || !strings.Contains(out, "$0.15 per month") {
		t.Error("stacked quota windows not rendered in admin table")
	}
	if !strings.Contains(out, "30 days") {
		t.Error("per-profile expiry not rendered in admin table")
	}
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields", forms, toks)
	}
}

// The Admins tab lists every account that holds the panel, offers a remove
// button on everyone but the acting admin, names accounts rather than
// addresses in its forms, and says so when nobody is an admin.
func TestAdminTemplateRendersAdminsTab(t *testing.T) {
	tmpl := parseAdminTemplate()

	render := func(d adminData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, d); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	withAdmins := render(adminData{
		Lang:      i18n.EN,
		Profiles:  []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		CSRFToken: "TOK",
		Admins: []adminRow{
			{User: database.User{ID: 1, Name: "Acting", Email: "acting@uni-osnabrueck.de", OIDCSub: "sub-acting"}, IsSelf: true},
			{User: database.User{ID: 2, Name: "Other", Email: "other@uni-osnabrueck.de", OIDCSub: "sub-other"}, Removable: true},
		},
		Candidates: []database.User{{ID: 3, Name: "Student", Email: "student@uni-osnabrueck.de"}},
	})
	for _, want := range []string{"acting@uni-osnabrueck.de", "other@uni-osnabrueck.de", "sub-other"} {
		if !strings.Contains(withAdmins, want) {
			t.Errorf("admin rows do not show %q", want)
		}
	}
	if n := strings.Count(withAdmins, `action="/admin/admins/revoke"`); n != 1 {
		t.Errorf("%d remove forms, want one: the acting admin's own row offers none", n)
	}
	if !strings.Contains(withAdmins, `name="user_id" value="2"`) {
		t.Error("the remove form does not name the account")
	}
	if !strings.Contains(withAdmins, `<option value="3">Student &lt;student@uni-osnabrueck.de&gt;</option>`) {
		t.Error("the promotion form does not offer the candidate account")
	}

	empty := render(adminData{
		Lang:      i18n.EN,
		Profiles:  []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		CSRFToken: "TOK",
	})
	if !strings.Contains(empty, "No account holds admin rights.") {
		t.Error("empty-admins fallback row did not render")
	}
}

// When the login system decides, the Admins tab names the group, lists who
// held the panel at their last login, and offers nothing to grant or remove.
func TestAdminTemplateRendersGroupManagedAdminsTab(t *testing.T) {
	tmpl := parseAdminTemplate()
	for _, lang := range []i18n.Lang{i18n.EN, i18n.DE} {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, adminData{
			Lang:      lang,
			Profiles:  []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
			CSRFToken: "TOK",
			Admins: []adminRow{
				{User: database.User{ID: 1, Name: "Acting", Email: "acting@uni-osnabrueck.de"}, IsSelf: true},
				{User: database.User{ID: 2, Name: "Other", Email: "other@uni-osnabrueck.de"}, Removable: true},
			},
			Candidates: []database.User{{ID: 3, Name: "Student", Email: "student@uni-osnabrueck.de"}},
			AdminGroup: "ai-admin",
		}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "other@uni-osnabrueck.de") {
			t.Errorf("%s: the admins are not listed", lang)
		}
		if !strings.Contains(out, "ai-admin") {
			t.Errorf("%s: the tab does not name the group", lang)
		}
		for _, form := range []string{`action="/admin/admins/revoke"`, `action="/admin/admins"`} {
			if strings.Contains(out, form) {
				t.Errorf("%s: the tab offers %s while the login system decides", lang, form)
			}
		}
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
		Quotas:     []quotaLine{{Budget: "$0.15", Period: "per day"}}, ProfileName: "students",
		BudgetUnit: "$",
		CSRFToken:  "T",
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
		Admins: []adminRow{
			{User: database.User{ID: 1, Name: "A", Email: "a@x.de"}, IsSelf: true},
			{User: database.User{ID: 3, Name: "B", Email: "b@x.de"}, Removable: true},
		},
		Candidates: []database.User{{ID: 2, Name: "U", Email: "u@x.de"}},
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
		"Usage limit (cost)",
		"Make someone an admin",
		"Choose an account",
		"Filter by name or email",
		"Make admin",
	} {
		if strings.Contains(out, english) {
			t.Errorf("untranslated on the German page: %q", english)
		}
	}
	// And confirm the German actually rendered.
	for _, german := range []string{"Benutzende", "Audit-Log", "Token pro Minute", "Administrator", "Konto auswählen"} {
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

// The admin tables have more columns than fit a phone, and quota windows
// stack inside their own cell. On a narrow screen they must overflow and
// scroll: compressing them clipped the quota column off the right edge, so an
// admin could not see the windows they had just saved.
//
// The floor is a fixed width, not max-content: max-content kept every cell on
// one line, which made the tables several times wider than the page on a
// desktop too.
func TestAdminTableCanScroll(t *testing.T) {
	css, err := fs.ReadFile(web.StaticFS, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)

	if !strings.Contains(style, ".table-wrap { overflow-x: auto; }") {
		t.Error("table wrapper does not scroll horizontally")
	}
	if !strings.Contains(style, "min-width: 40rem;") {
		t.Error("table has no min-width, so it compresses rather than scrolling")
	}
	if strings.Contains(style, "min-width: max-content") {
		t.Error("table cells may not wrap, so the tables outgrow a desktop screen")
	}
}

// The browser draws its own controls (date picker, spinners, checkboxes) in
// the scheme the page declares. Without the declaration they stay light on
// the dark theme.
func TestStyleDeclaresColorScheme(t *testing.T) {
	css, err := fs.ReadFile(web.StaticFS, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"color-scheme: light;", "color-scheme: dark;"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("style.css does not declare %q", want)
		}
	}
}

// The edit forms open as dialogs over the tables rather than sitting in every
// row (users) or below the table (profiles), which made the users table
// wider than any screen and scrolled the page away from the profile being
// edited.
func TestAdminEditFormsAreDialogs(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Lang:      i18n.EN,
		Profiles:  []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
		Users:     []userRow{{User: database.User{ID: 7, Name: "U", Email: "u@uni-osnabrueck.de"}}},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		`<dialog id="profile-dialog"`,
		`<dialog id="user-dialog-7"`,
		`document.getElementById('user-dialog-7').showModal()`,
		`<main class="main-wide">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("admin page is missing %q", want)
		}
	}
	// The user's form must be inside its dialog, not left in the table row.
	dialog := out[strings.Index(out, `<dialog id="user-dialog-7"`):]
	dialog = dialog[:strings.Index(dialog, "</dialog>")]
	if !strings.Contains(dialog, `action="/admin/users/7/profile"`) {
		t.Error("the user's profile form is not inside its dialog")
	}
}

// Every row's actions sit in a ⋮ menu of the same shape. An action the row
// does not allow stays in the menu, greyed out with the reason, rather than
// disappearing and leaving the admin to guess why.
func TestAdminRowMenusExplainDisabledActions(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Lang: i18n.EN,
		Profiles: []database.Profile{
			{ID: 1, Name: "default", IsDefault: true},
			{ID: 2, Name: "teaching"},
		},
		Users: []userRow{
			{User: database.User{ID: 7, Email: "nokey@uni-osnabrueck.de"}},
			{User: database.User{ID: 8, Email: "key@uni-osnabrueck.de"}, HasKey: true, KeyPrefix: "sk-a"},
		},
		Admins: []adminRow{
			{User: database.User{ID: 1, Email: "self@uni-osnabrueck.de"}, IsSelf: true},
			{User: database.User{ID: 2, Email: "other@uni-osnabrueck.de"}, Removable: true},
		},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := html.UnescapeString(buf.String())

	for _, id := range []string{"profile-menu-1", "profile-menu-2", "user-menu-7", "user-menu-8", "admin-menu-1", "admin-menu-2"} {
		if !strings.Contains(out, `popovertarget="`+id+`"`) || !strings.Contains(out, `id="`+id+`" class="row-menu" popover`) {
			t.Errorf("row menu %s is missing its button or its popover", id)
		}
	}
	for _, reason := range []string{
		"The default profile cannot be deleted.",
		"There is no key.",
		"Nobody can remove their own rights.",
	} {
		if !strings.Contains(out, reason) {
			t.Errorf("a greyed-out entry does not say %q", reason)
		}
	}
	// The allowed actions are still real forms.
	for _, action := range []string{`action="/admin/profiles/2/delete"`, `action="/admin/users/8/key/revoke"`, `action="/admin/admins/revoke"`} {
		if !strings.Contains(out, action) {
			t.Errorf("menu is missing the form %s", action)
		}
	}
	if strings.Contains(out, `action="/admin/profiles/1/delete"`) || strings.Contains(out, `action="/admin/users/7/key/revoke"`) {
		t.Error("a disabled action still renders a form that would post")
	}
}

// With a deadline set, the users table says what happens at it, so the admin
// sees the whole assignment without opening the dialog.
func TestAdminUserRowSummarisesDeadline(t *testing.T) {
	render := func(row userRow) string {
		t.Helper()
		var buf bytes.Buffer
		if err := parseAdminTemplate().Execute(&buf, adminData{
			Lang: i18n.EN,
			Profiles: []database.Profile{
				{ID: 1, Name: "default", IsDefault: true},
				{ID: 2, Name: "teaching"},
			},
			Users: []userRow{row},
		}); err != nil {
			t.Fatal(err)
		}
		// Collapse the template's line breaks to compare the sentence.
		return strings.Join(strings.Fields(html.UnescapeString(buf.String())), " ")
	}

	out := render(userRow{
		User:          database.User{ID: 1, Email: "a@uni-osnabrueck.de"},
		ExpiresAt:     "2026-12-31",
		AfterExpiryID: 2,
	})
	if !strings.Contains(out, "until 2026-12-31, then teaching") {
		t.Error("the row does not name the deadline and the profile that follows")
	}

	out = render(userRow{
		User:           database.User{ID: 1, Email: "a@uni-osnabrueck.de"},
		ExpiresAt:      "2026-12-31",
		RevokeAtExpiry: true,
	})
	if !strings.Contains(out, "until 2026-12-31, then the key is deleted") {
		t.Error("the row does not say the key is deleted at the deadline")
	}

	out = render(userRow{User: database.User{ID: 1, Email: "a@uni-osnabrueck.de"}})
	if strings.Contains(out, "until 2026") {
		t.Error("a permanent assignment shows a deadline")
	}

	// Without a profile to switch to, the user goes back to following the
	// default; naming the profile that is the default today would be wrong
	// once another one becomes the default.
	out = render(userRow{
		User:      database.User{ID: 1, Email: "a@uni-osnabrueck.de"},
		ExpiresAt: "2026-12-31",
	})
	if !strings.Contains(out, "until 2026-12-31, then back to the default") {
		t.Error("the row does not say the user returns to the default")
	}
}

// "Follow the default" and the profile that is the default today are two
// different choices: the first moves with the default, the second stays put.
// Both read "default" otherwise, so the first says what it does.
func TestAdminFollowDefaultIsLabelledApart(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Lang: i18n.EN,
		Profiles: []database.Profile{
			{ID: 1, Name: "basic", IsDefault: true},
			{ID: 2, Name: "teaching"},
		},
		Users:     []userRow{{User: database.User{ID: 7, Email: "a@uni-osnabrueck.de", OIDCSub: "sub-7"}}},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := html.UnescapeString(buf.String())

	// Once for the profile, once for what follows a deadline.
	if n := strings.Count(out, `<option value="0" selected>Follow the default (currently: basic)</option>`); n != 1 {
		t.Errorf("profile dropdown labels the follow option %d times, want once", n)
	}
	if !strings.Contains(out, `<option value="0">Follow the default (currently: basic)</option>`) {
		t.Error("the after-deadline dropdown does not label the follow option")
	}
	if !strings.Contains(out, "follows the default") {
		t.Error("the table does not say the user follows the default")
	}
	// The subject is a value to read, not a control: no button, no copy.
	if strings.Contains(out, `data-model="sub-7"`) || strings.Contains(out, "copyModel") {
		t.Error("the OIDC subject is still rendered as a copy button")
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
				{Budget: 0.001, Period: "1h"},
				{Budget: 0.1, Period: "30d"},
			},
		}},
		Users:      []userRow{},
		CSRFToken:  "TOK",
		BudgetUnit: "$",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := html.UnescapeString(buf.String())

	// The table cell renders each window in the admin's own units.
	for _, want := range []string{"$0.0010", "$0.10"} {
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
		Lang:       i18n.EN,
		Langs:      i18n.Supported,
		User:       &database.User{Email: "a@b.c"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-x", ExpiresAt: time.Now().Add(24 * time.Hour)},
		BudgetUnit: "$",
		Usage: usageReport{
			HasQuota: true,
			Windows: []quotaWindowView{
				{Period: "1h", Label: "per hour", Used: 0.00095, Limit: 0.001, Pct: 95},
				{Period: "30d", Label: "per month", Used: 0.00959, Limit: 1, Pct: 1},
				// Touched, but a hundredth of a percent: must not read as 0%.
				{Period: "7d", Label: "per week", Used: 0.0000483, Limit: 5, Pct: 0},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := html.UnescapeString(buf.String())

	if got := strings.Count(out, "quota-fill"); got != 3 {
		t.Errorf("drew %d bars, want one per window", got)
	}
	// The nearly-spent window is marked as full so it reads as urgent.
	if !strings.Contains(out, "quota-full") {
		t.Error("the 95% window is not marked as nearly exhausted")
	}
	for _, want := range []string{"per hour", "per month", "width:95%", "width:1%", "95%", "$0.0010", "$1.00", "<1%", "<$0.0001"} {
		if !strings.Contains(out, want) {
			t.Errorf("card is missing %q", want)
		}
	}
}

// With no per-window figures the card keeps the single bar rather than showing
// nothing, so a gateway that cannot report every window still reports a quota.
func TestDashboardFallsBackToOneBar(t *testing.T) {
	var buf bytes.Buffer
	err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:       i18n.EN,
		Langs:      i18n.Supported,
		User:       &database.User{Email: "a@b.c"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-x", ExpiresAt: time.Now().Add(24 * time.Hour)},
		BudgetUnit: "$",
		Usage: usageReport{
			HasQuota: true, Used: 0.00959, Limit: 1, QuotaPct: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(buf.String(), "quota-fill"); got != 1 {
		t.Errorf("drew %d bars, want exactly one in the fallback", got)
	}
}

// The admin panel shows each user's OIDC subject, the identifier that stays
// with the person: an email address can be reassigned by the IdP. Without
// this an operator would have to query the database to tell accounts apart.
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

// The deadline's follow-up controls mean nothing without a date, so a row with
// no deadline renders them hidden and a row with one renders them visible.
// Asserted here because the visibility is server-rendered: the JS only keeps it
// in step afterwards, so a broken template would ship two dead controls on
// every user row with nothing failing.
func TestAdminTemplateHidesExpiryOptionsWithoutADate(t *testing.T) {
	tmpl := parseAdminTemplate()

	render := func(row userRow) string {
		t.Helper()
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, adminData{
			Lang:     i18n.EN,
			Profiles: []database.Profile{{ID: 1, Name: "default", IsDefault: true}},
			Users:    []userRow{row},
		}); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	permanent := render(userRow{User: database.User{ID: 1, Email: "a@uni-osnabrueck.de"}})
	if !strings.Contains(permanent, `class="expiry-options" hidden`) {
		t.Error("a row with no deadline did not hide the follow-up controls")
	}

	dated := render(userRow{
		User:      database.User{ID: 2, Email: "b@uni-osnabrueck.de"},
		ExpiresAt: "2026-10-04",
	})
	if strings.Contains(dated, `class="expiry-options" hidden`) {
		t.Error("a row with a deadline hid the follow-up controls")
	}
	// The controls must still be present in both cases: hiding is not removing,
	// and the form posts them either way.
	if !strings.Contains(permanent, `name="after_expiry"`) {
		t.Error("the destination dropdown was omitted rather than hidden")
	}
}
