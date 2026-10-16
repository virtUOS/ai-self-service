package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/i18n"
)

// getAccount renders the settings page for the test user, with cookies.
func getAccount(t *testing.T, ui *UI, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	ui.Account(rec, req)
	return rec
}

// saveAccount posts the settings form for the test user. CSRF is the
// router's job, so the handler is called directly.
func saveAccount(t *testing.T, ui *UI, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/account", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := httptest.NewRecorder()
	ui.SaveAccount(rec, req)
	return rec
}

// selected returns the value of the selected option in the select named name.
func selected(t *testing.T, page, name string) string {
	t.Helper()
	i := strings.Index(page, `name="`+name+`"`)
	if i < 0 {
		t.Fatalf("no %s select", name)
	}
	sel := page[i : i+strings.Index(page[i:], "</select>")]
	m := regexp.MustCompile(`<option value="([^"]*)"[^>]*\bselected\b`).FindAllStringSubmatch(sel, -1)
	if len(m) != 1 {
		t.Fatalf("%s select has %d options selected, want 1", name, len(m))
	}
	return m[0][1]
}

// The page shows who the portal knows the user as: the identifiers an
// administrator or the gateway's logs would refer to.
func TestAccountPageShowsTheAccount(t *testing.T) {
	ui, _, _, user := newTestUI(t, "acct1")

	rec := getAccount(t, ui, "/account", &http.Cookie{Name: i18n.CookieName, Value: "en"})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{user.Name, user.Email, user.OIDCSub, "User ID", "OIDC subject"} {
		if !strings.Contains(page, want) {
			t.Errorf("account page does not show %q", want)
		}
	}
	// The form posts back here, CSRF-protected like every other form.
	if !strings.Contains(page, `action="/account"`) || !strings.Contains(page, `name="csrf_token"`) {
		t.Error("settings form is missing or has no CSRF token")
	}
	// The menu does not link the page it is on.
	if strings.Contains(accountMenu(page), `href="/account"`) {
		t.Error("menu links the settings from the settings")
	}
}

func TestAccountPageNeedsALogin(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct2")
	rec := httptest.NewRecorder()
	ui.Account(rec, httptest.NewRequest(http.MethodGet, "/account", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous visitor got %d to %q, want a redirect to /login",
			rec.Code, rec.Header().Get("Location"))
	}
}

// Without a choice both settings say "system", even when the browser asks for
// a language the portal has: following the browser is not the same as having
// picked German.
func TestAccountPageSelectsTheCurrentChoices(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct3")

	page := getAccount(t, ui, "/account").Body.String()
	if got := selected(t, page, "lang"); got != "" {
		t.Errorf("language %q selected without a choice, want system", got)
	}
	if got := selected(t, page, "theme"); got != "" {
		t.Errorf("theme %q selected without a choice, want system", got)
	}
	// Every language is offered under its own name.
	for _, l := range i18n.Supported {
		if !strings.Contains(page, `<option value="`+string(l)+`" lang="`+string(l)+`"`) ||
			!strings.Contains(page, l.Name()) {
			t.Errorf("language %s is not offered", l)
		}
	}

	page = getAccount(t, ui, "/account",
		&http.Cookie{Name: i18n.CookieName, Value: "en"},
		&http.Cookie{Name: themeCookie, Value: "dark"}).Body.String()
	if got := selected(t, page, "lang"); got != "en" {
		t.Errorf("language %q selected, want en", got)
	}
	if got := selected(t, page, "theme"); got != "dark" {
		t.Errorf("theme %q selected, want dark", got)
	}
}

// A choice is kept for a year; "system" deletes the cookie, so the browser
// and the operating system decide again.
func TestSaveAccountStoresAndClearsTheChoices(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct4")

	cookies := func(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
		m := map[string]*http.Cookie{}
		for _, c := range rec.Result().Cookies() {
			m[c.Name] = c
		}
		return m
	}

	rec := saveAccount(t, ui, url.Values{"lang": {"en"}, "theme": {"light"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?saved=1" {
		t.Fatalf("got %d to %q, want a redirect back to the settings",
			rec.Code, rec.Header().Get("Location"))
	}
	c := cookies(rec)
	if c[i18n.CookieName] == nil || c[i18n.CookieName].Value != "en" || c[i18n.CookieName].MaxAge <= 0 {
		t.Errorf("language not stored: %+v", c[i18n.CookieName])
	}
	if c[themeCookie] == nil || c[themeCookie].Value != "light" || c[themeCookie].MaxAge <= 0 {
		t.Errorf("theme not stored: %+v", c[themeCookie])
	}

	c = cookies(saveAccount(t, ui, url.Values{"lang": {""}, "theme": {""}}))
	for _, name := range []string{i18n.CookieName, themeCookie} {
		if c[name] == nil || c[name].MaxAge >= 0 {
			t.Errorf("choosing system does not delete the %s cookie: %+v", name, c[name])
		}
	}
}

// The form offers nothing else, so another value is a crafted request.
func TestSaveAccountRejectsUnknownValues(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct5")
	for _, form := range []url.Values{
		{"lang": {"klingon"}, "theme": {""}},
		{"lang": {""}, "theme": {"sepia"}},
	} {
		rec := saveAccount(t, ui, form)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%v got %d, want 400", form, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%v still set a cookie", form)
		}
	}
}

// The confirmation shows once, after saving.
func TestAccountPageConfirmsTheSave(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct6")
	en := &http.Cookie{Name: i18n.CookieName, Value: "en"}
	if strings.Contains(getAccount(t, ui, "/account", en).Body.String(), "have been saved") {
		t.Error("confirmation shown without a save")
	}
	if !strings.Contains(getAccount(t, ui, "/account?saved=1", en).Body.String(), "have been saved") {
		t.Error("no confirmation after a save")
	}
}

// A chosen theme is on <html> when the page arrives, so it applies before the
// first paint; without one the page has no attribute and follows the system.
func TestPagesCarryTheChosenTheme(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct7")

	dashboard := func(cookies ...*http.Cookie) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		ui.Dashboard(rec, req)
		return rec.Body.String()
	}

	page := dashboard()
	if strings.Contains(page, "data-theme") {
		t.Error("page pins a theme without a choice")
	}
	if strings.Count(page, `name="theme-color"`) != 2 {
		t.Error("without a choice the browser bar does not follow the system")
	}

	page = dashboard(&http.Cookie{Name: themeCookie, Value: "dark"})
	if !strings.Contains(page, `data-theme="dark"`) {
		t.Error("chosen dark theme is not on the page")
	}
	if strings.Count(page, `name="theme-color"`) != 1 || !strings.Contains(page, `<meta name="theme-color" content="#121817">`) {
		t.Error("browser bar does not follow the chosen theme")
	}

	// Only the known values reach the markup.
	if strings.Contains(dashboard(&http.Cookie{Name: themeCookie, Value: `x"><script>`}), "data-theme") {
		t.Error("an unknown theme cookie reached the page")
	}
}

// Nothing user-facing may remain in English on the German page.
func TestAccountPageFullyGerman(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "acct8")
	page := getAccount(t, ui, "/account?saved=1", &http.Cookie{Name: i18n.CookieName, Value: "de"}).Body.String()
	for _, english := range []string{"Account settings", "Appearance", "Username", "User ID", ">Theme<", ">Light<", ">Dark<", ">Save<", "System default", "have been saved"} {
		if strings.Contains(page, english) {
			t.Errorf("untranslated on the German page: %q", english)
		}
	}
	for _, german := range []string{"Kontoeinstellungen", "Darstellung", "Farbschema", "Hell", "Dunkel", "Speichern"} {
		if !strings.Contains(page, german) {
			t.Errorf("expected German text %q missing", german)
		}
	}
}
