package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
)

func renderDashboard(t *testing.T, lang i18n.Lang) string {
	t.Helper()
	var buf bytes.Buffer
	err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:  lang,
		Langs: i18n.Supported,
		Path:  "/",
		User:  &database.User{Name: "T", Email: "t@uni.de"},
		APIKey: &database.APIKey{
			KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
		APIBaseURL: "https://litellm.example.com/v1",
		CSRFToken:  "T",
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The same page must render in either language.
func TestDashboardRendersBothLanguages(t *testing.T) {
	de := renderDashboard(t, i18n.DE)
	if !strings.Contains(de, "Ihr Konto") {
		t.Error("German dashboard missing translated heading")
	}
	if strings.Contains(de, "Your account") {
		t.Error("German dashboard still shows English")
	}

	en := renderDashboard(t, i18n.EN)
	if !strings.Contains(en, "Your account") {
		t.Error("English dashboard missing heading")
	}
	if strings.Contains(en, "Ihr Konto") {
		t.Error("English dashboard shows German")
	}
}

// The switcher offers the other language, not the current one.
func TestSwitcherOffersOtherLanguage(t *testing.T) {
	de := renderDashboard(t, i18n.DE)
	if !strings.Contains(de, `value="en"`) {
		t.Error("German page does not offer English")
	}
	if strings.Contains(de, `name="lang" value="de"`) {
		t.Error("German page offers German, which does nothing")
	}
}

func TestSetLanguageStoresChoice(t *testing.T) {
	h := SetLanguage(false)
	req := httptest.NewRequest(http.MethodPost, "/lang",
		strings.NewReader(url.Values{"lang": {"en"}, "return_to": {"/admin"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("got %d, want a redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Errorf("returned to %q, want /admin", loc)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == i18n.CookieName && c.Value == "en" {
			found = true
			if c.MaxAge <= 0 {
				t.Error("language choice does not persist across sessions")
			}
		}
	}
	if !found {
		t.Error("no language cookie set")
	}
}

// return_to must not become an open redirect.
func TestSetLanguageRejectsOffsiteReturn(t *testing.T) {
	for _, bad := range []string{"https://evil.example", "//evil.example", "javascript:alert(1)"} {
		req := httptest.NewRequest(http.MethodPost, "/lang",
			strings.NewReader(url.Values{"lang": {"de"}, "return_to": {bad}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		SetLanguage(false)(rec, req)
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("return_to %q redirected to %q, want /", bad, loc)
		}
	}
}

// An unsupported language falls back rather than rendering keys.
func TestSetLanguageRejectsUnknown(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/lang",
		strings.NewReader(url.Values{"lang": {"klingon"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	SetLanguage(false)(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == i18n.CookieName && c.Value != string(i18n.Default) {
			t.Errorf("stored unsupported language %q", c.Value)
		}
	}
}
