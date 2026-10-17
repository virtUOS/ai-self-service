package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/i18n"
)

// getPrivacy requests the privacy page as an anonymous visitor, in lang when
// one is given.
func getPrivacy(t *testing.T, ui *UI, lang i18n.Lang) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	if lang != "" {
		req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: string(lang)})
	}
	rec := httptest.NewRecorder()
	ui.Privacy(rec, req)
	return rec
}

// The notice has to be readable before anyone signs in, since signing in is
// where the data it describes starts being collected, and other sites link to
// it. The operator's markup is shown as markup, not as escaped text.
func TestPrivacyPageShowsTheNoticeWithoutLogin(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "priv1")
	ui.cfg.PrivacyNoticeDE = `<h2>Ihre Anfragen</h2><p>Siehe die ` +
		`<a href="https://www.uni-osnabrueck.de/datenschutzerklaerung">Datenschutzerklärung</a>.</p>`

	rec := getPrivacy(t, ui, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous visitor got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<h2>Ihre Anfragen</h2>`,
		`<a href="https://www.uni-osnabrueck.de/datenschutzerklaerung">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not render %s as markup", want)
		}
	}
	// The page's own chrome is translated; a missing key renders as itself.
	if strings.Contains(body, "privacy.title") {
		t.Error("page title rendered as a raw catalogue key")
	}
}

// A deployment without a notice has no privacy page, rather than an empty one.
func TestPrivacyPageIsNotFoundWhenUnconfigured(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "priv2")

	if rec := getPrivacy(t, ui, ""); rec.Code != http.StatusNotFound {
		t.Errorf("unconfigured privacy page returned %d, want 404", rec.Code)
	}
}

// Readers get the notice in their language. When only one language has been
// written they get that one, since a notice in the other language beats none.
func TestPrivacyNoticeFollowsTheReadersLanguage(t *testing.T) {
	const de, en = "<p>Deutscher Text</p>", "<p>English text</p>"
	for _, tc := range []struct {
		name       string
		haveDE     string
		haveEN     string
		reader     i18n.Lang
		wantNotice string
	}{
		{"German reader, both written", de, en, i18n.DE, de},
		{"English reader, both written", de, en, i18n.EN, en},
		{"English reader, German only", de, "", i18n.EN, de},
		{"German reader, English only", "", en, i18n.DE, en},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui, _, _, _ := newTestUI(t, "priv3-"+strings.ReplaceAll(tc.name, " ", ""))
			ui.cfg.PrivacyNoticeDE, ui.cfg.PrivacyNoticeEN = tc.haveDE, tc.haveEN

			rec := getPrivacy(t, ui, tc.reader)

			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantNotice) {
				t.Errorf("page does not show %s", tc.wantNotice)
			}
		})
	}
}

// Once a notice exists, users find it in the footer of every page and on the
// key card, where they are about to create the key it is about. Without one
// there is nothing to link to.
func TestDashboardLinksThePrivacyNoticeWhenConfigured(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "priv4")
	render := func() (main, footer string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
		rec := httptest.NewRecorder()
		ui.Dashboard(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("dashboard returned %d", rec.Code)
		}
		_, rest, ok := strings.Cut(rec.Body.String(), "<main>")
		if !ok {
			t.Fatal("dashboard has no <main>")
		}
		main, footer, _ = strings.Cut(rest, "</main>")
		return main, footer
	}

	if main, footer := render(); strings.Contains(main+footer, `href="/privacy"`) {
		t.Error("dashboard links a privacy page that does not exist")
	}

	ui.cfg.PrivacyNoticeDE = "<p>Hinweise</p>"
	main, footer := render()
	if !strings.Contains(footer, `<footer>`) || !strings.Contains(footer, `href="/privacy"`) {
		t.Error("footer does not link the privacy notice")
	}
	if !strings.Contains(main, `href="/privacy"`) {
		t.Error("key card does not link the privacy notice")
	}
}

// tabs returns the privacy page's language tabs as "lang:active" pairs, in
// order, or nil when the page has none.
func tabs(page string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<a class="tab-btn( active)?" href="/privacy\?lang=(\w+)"`).FindAllStringSubmatch(page, -1) {
		out = append(out, m[2]+":"+strconv.FormatBool(m[1] != ""))
	}
	return out
}

// An anonymous reader can switch the notice between the languages it is
// written in. The default is their own language, as for the rest of the page.
func TestPrivacyPageOffersTheNoticesLanguages(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "priv5")
	ui.cfg.PrivacyNoticeDE, ui.cfg.PrivacyNoticeEN = "<p>Deutscher Text</p>", "<p>English text</p>"

	get := func(path, acceptLang string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if acceptLang != "" {
			req.Header.Set("Accept-Language", acceptLang)
		}
		rec := httptest.NewRecorder()
		ui.Privacy(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s got %d", path, rec.Code)
		}
		return rec.Body.String()
	}

	page := get("/privacy", "en-GB,en;q=0.9")
	if got := strings.Join(tabs(page), " "); got != "de:false en:true" {
		t.Errorf("tabs = %q, want both languages with English shown", got)
	}
	if !strings.Contains(page, "English text") || strings.Contains(page, "Deutscher Text") {
		t.Error("English reader does not get the English notice")
	}

	// A tab shows its language, page and all, and remembers nothing.
	req := httptest.NewRequest(http.MethodGet, "/privacy?lang=de", nil)
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	ui.Privacy(rec, req)
	page = rec.Body.String()
	if got := strings.Join(tabs(page), " "); got != "de:true en:false" {
		t.Errorf("tabs = %q, want German shown", got)
	}
	if !strings.Contains(page, "Deutscher Text") || !strings.Contains(page, `<html lang="de"`) {
		t.Error("?lang=de does not show the German page")
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == i18n.CookieName {
			t.Error("a tab stored the language")
		}
	}

	// An unknown language is ignored rather than rendering keys.
	if !strings.Contains(get("/privacy?lang=xx", "en"), "English text") {
		t.Error("an unknown ?lang changed the notice")
	}
}

// One notice needs no tabs, and a signed-in reader gets the language of the
// rest of the interface, with a link to the account settings rather than tabs.
func TestPrivacyPageTabsOnlyWhenThereIsAChoice(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "priv6")
	ui.cfg.PrivacyNoticeDE = "<p>Deutscher Text</p>"
	page := getPrivacy(t, ui, i18n.EN).Body.String()
	if got := tabs(page); got != nil {
		t.Errorf("tabs %v for a notice in one language", got)
	}
	if strings.Contains(page, `class="notice-lang"`) {
		t.Error("language link for a notice in one language")
	}

	ui.cfg.PrivacyNoticeEN = "<p>English text</p>"
	req := httptest.NewRequest(http.MethodGet, "/privacy?lang=de", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "en"})
	rec := httptest.NewRecorder()
	ui.Privacy(rec, req)
	page = rec.Body.String()
	if got := tabs(page); got != nil {
		t.Errorf("signed-in reader got tabs %v", got)
	}
	if !strings.Contains(page, `<p class="notice-lang"><a href="/account">`) {
		t.Error("signed-in reader gets no link to the account settings")
	}
	if !strings.Contains(page, "English text") {
		t.Error("signed-in reader does not get the language they chose")
	}
}

// A notice shown in another language than the page says so, for screen
// readers and hyphenation.
func TestPrivacyNoticeIsMarkedWithItsLanguage(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "priv7")
	ui.cfg.PrivacyNoticeDE = "<p>Deutscher Text</p>"
	page := getPrivacy(t, ui, i18n.EN).Body.String()
	if !strings.Contains(page, `<html lang="en"`) || !strings.Contains(page, `<div class="notice" lang="de">`) {
		t.Error("German notice on an English page is not marked as German")
	}
}
