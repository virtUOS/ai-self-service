package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
)

// accountMenu returns the popover's markup, or "" when the page has none.
func accountMenu(page string) string {
	i := strings.Index(page, `id="account-menu"`)
	if i < 0 {
		return ""
	}
	menu := page[i:]
	return menu[:strings.Index(menu, "</header>")]
}

// The menu carries what used to crowd the header: who is signed in, the way
// to the admin panel for those allowed in, and sign-out.
func TestDashboardAccountMenu(t *testing.T) {
	for _, isAdmin := range []bool{false, true} {
		var buf bytes.Buffer
		if err := parseDashboardTemplate().Execute(&buf, dashboardData{
			Lang:      i18n.EN,
			Path:      "/",
			User:      &database.User{Name: "lkiesow", Email: "l@uni-osnabrueck.de"},
			IsAdmin:   isAdmin,
			CSRFToken: "TOK",
		}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		page := buf.String()

		if !strings.Contains(page, `popovertarget="account-menu"`) {
			t.Fatal("no account menu trigger")
		}
		// On a phone the trigger shrinks to the avatar, so the menu says who
		// is signed in.
		if !strings.Contains(page, `<span class="avatar" aria-hidden="true">L</span>`) {
			t.Error("trigger has no avatar with the user's initial")
		}
		menu := accountMenu(page)
		if !strings.Contains(menu, "lkiesow") || !strings.Contains(menu, "l@uni-osnabrueck.de") {
			t.Error("menu does not show who is signed in")
		}
		if !strings.Contains(menu, `href="/account"`) {
			t.Error("menu does not lead to the account settings")
		}
		if !strings.Contains(menu, `action="/logout"`) || !strings.Contains(menu, `value="TOK"`) {
			t.Error("menu has no CSRF-protected sign-out form")
		}
		if got := strings.Contains(menu, `href="/admin"`); got != isAdmin {
			t.Errorf("admin=%v but Admin entry shown=%v", isAdmin, got)
		}
		if strings.Contains(menu, `href="/"`) {
			t.Error("menu links the dashboard from the dashboard")
		}
	}
}

// The language is chosen in the account settings now; the header has no
// switch left to crowd it on a phone.
func TestHeaderHasNoLanguageSwitch(t *testing.T) {
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang: i18n.DE, Path: "/", CSRFToken: "TOK",
		User: &database.User{Name: "lkiesow", Email: "l@uni-osnabrueck.de"},
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	page := buf.String()
	header := page[strings.Index(page, "<header>"):strings.Index(page, "</header>")]
	if strings.Contains(header, `name="lang"`) || strings.Contains(header, `action="/lang"`) {
		t.Error("header still has a language switch")
	}
}

// The admin page used to have no sign-out at all.
func TestAdminPageAccountMenu(t *testing.T) {
	var buf bytes.Buffer
	if err := parseAdminTemplate().Execute(&buf, adminData{
		Lang:      i18n.EN,
		Path:      "/admin",
		TitleKey:  "admin.title",
		User:      &database.User{Name: "lkiesow", Email: "l@uni-osnabrueck.de"},
		IsAdmin:   true,
		CSRFToken: "TOK",
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	menu := accountMenu(buf.String())
	if !strings.Contains(menu, `action="/logout"`) {
		t.Error("admin page cannot sign out")
	}
	if !strings.Contains(menu, `href="/"`) {
		t.Error("menu does not lead back to the dashboard")
	}
	if strings.Contains(menu, `href="/admin"`) {
		t.Error("menu links the admin page from the admin page")
	}
}

// The privacy page is public. An anonymous reader gets no account menu, a
// signed-in one keeps theirs.
func TestPrivacyPageAccountMenuFollowsSession(t *testing.T) {
	ui, _, _, user := newTestUI(t, "hdr1")
	ui.cfg.PrivacyNoticeDE = "<p>Hinweise</p>"

	anon := getPrivacy(t, ui, "").Body.String()
	if accountMenu(anon) != "" {
		t.Error("anonymous reader got an account menu")
	}
	if !strings.Contains(anon, `<a href="/">`) {
		t.Error("anonymous reader has no way to the dashboard")
	}

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := httptest.NewRecorder()
	ui.Privacy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed-in reader got %d", rec.Code)
	}
	page := rec.Body.String()
	if !strings.Contains(accountMenu(page), user.Email) {
		t.Error("signed-in reader lost the account menu")
	}
	// The page does not link itself.
	if strings.Contains(page, "<footer>") {
		t.Error("privacy page links to itself from the footer")
	}
}
