//go:build browser

package browsertest

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// The avatar opens the account menu, which names the user and links the other
// pages, not the one it is on.
func TestAccountMenu(t *testing.T) {
	t.Parallel()
	links := func(t *testing.T, page playwright.Page) []string {
		t.Helper()
		click(t, page.Locator(".account-trigger"))
		menu := page.Locator("#account-menu")
		check(t, expect.Locator(menu).ToBeVisible())
		texts, err := menu.Locator("a").AllTextContents()
		check(t, err)
		check(t, page.Keyboard().Press("Escape"))
		check(t, expect.Locator(menu).ToBeHidden())
		return texts
	}

	t.Run("user", func(t *testing.T) {
		t.Parallel()
		page := loggedIn(t, "student", pageOptions{})
		check(t, expect.Locator(page.Locator(".avatar")).ToHaveText("T"))
		click(t, page.Locator(".account-trigger"))
		check(t, expect.Locator(page.Locator("#account-menu")).ToContainText("student@uni-osnabrueck.de"))
		check(t, page.Keyboard().Press("Escape"))
		if got := links(t, page); !slices.Equal(got, []string{"Account settings"}) {
			t.Errorf("menu on the dashboard links %q, want only the settings", got)
		}
		goTo(t, page, "/account")
		if got := links(t, page); !slices.Equal(got, []string{"Dashboard"}) {
			t.Errorf("menu on the settings links %q, want only the dashboard", got)
		}
	})
	t.Run("admin", func(t *testing.T) {
		t.Parallel()
		page := loggedIn(t, "admin", pageOptions{})
		if got := links(t, page); !slices.Equal(got, []string{"Admin", "Account settings"}) {
			t.Errorf("admin's menu on the dashboard links %q, want the admin panel and the settings", got)
		}
	})
}

// The language and theme chosen in the settings apply from the next page on,
// rendered by the server, and choosing the browser setting again follows the
// browser.
func TestAccountSettings(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "student", pageOptions{colorScheme: playwright.ColorSchemeDark})
	html := page.Locator("html")
	goTo(t, page, "/account")
	for _, want := range []string{"Test Student", "student@uni-osnabrueck.de", "student"} {
		check(t, expect.Locator(page.Locator("dl.info-grid")).ToContainText(want))
	}

	save := func(lang, theme string) {
		t.Helper()
		_, err := page.Locator("#setting-lang").SelectOption(playwright.SelectOptionValues{Values: &[]string{lang}})
		check(t, err)
		_, err = page.Locator("#setting-theme").SelectOption(playwright.SelectOptionValues{Values: &[]string{theme}})
		check(t, err)
		click(t, page.Locator("form[action='/account'] button[type='submit']"))
		check(t, expect.Page(page).ToHaveURL(seeded.URL+"/account?saved=1"))
	}

	save("de", "light")
	check(t, expect.Locator(html).ToHaveAttribute("lang", "de"))
	check(t, expect.Locator(html).ToHaveAttribute("data-theme", "light"))
	check(t, expect.Locator(page.Locator(".alert-success")).ToContainText("gespeichert"))
	goTo(t, page, "/")
	check(t, expect.Locator(html).ToHaveAttribute("lang", "de"))
	check(t, expect.Locator(html).ToHaveAttribute("data-theme", "light"))
	// Light although the browser asks for dark: the choice wins.
	bg, err := page.Evaluate("getComputedStyle(document.body).colorScheme")
	check(t, err)
	if bg != "light" {
		t.Errorf("color-scheme = %v with the light theme chosen", bg)
	}

	goTo(t, page, "/account")
	save("", "")
	check(t, expect.Locator(html).ToHaveAttribute("lang", "en"))
	check(t, expect.Locator(html).Not().ToHaveAttribute("data-theme", regexp.MustCompile(`.`)))
}

// Without a choice, the language follows the browser's.
func TestLanguageFollowsTheBrowser(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "student", pageOptions{locale: "de-DE"})
	check(t, expect.Locator(page.Locator("html")).ToHaveAttribute("lang", "de"))
	check(t, expect.Locator(page.GetByText("Ihr Konto")).ToBeVisible())
}

// Someone who is not signed in picks the notice's language with tabs, which
// store nothing; someone signed in reads it in their language and is pointed
// to the settings.
func TestPrivacyNotice(t *testing.T) {
	t.Parallel()
	t.Run("anonymous", func(t *testing.T) {
		t.Parallel()
		page := newPage(t, pageOptions{})
		goTo(t, page, "/privacy")
		tabs := page.Locator("nav.tabs a")
		check(t, expect.Locator(tabs).ToHaveCount(2))
		notice := page.Locator(".notice")
		check(t, expect.Locator(notice).ToHaveAttribute("lang", "en"))
		check(t, expect.Locator(notice).ToContainText("Privacy notice of the seeded instance"))

		before, err := page.Context().Cookies()
		check(t, err)
		click(t, page.Locator("nav.tabs a[lang='de']"))
		check(t, expect.Locator(notice).ToHaveAttribute("lang", "de"))
		check(t, expect.Locator(notice).ToContainText("Datenschutzhinweis der Seed-Instanz"))
		after, err := page.Context().Cookies()
		check(t, err)
		if len(after) != len(before) {
			t.Errorf("choosing a tab set cookies: %d before, %d after", len(before), len(after))
		}
	})
	t.Run("signed in", func(t *testing.T) {
		t.Parallel()
		page := loggedIn(t, "student", pageOptions{})
		goTo(t, page, "/privacy")
		check(t, expect.Locator(page.Locator("nav.tabs")).ToHaveCount(0))
		link := page.Locator("a[href='/account']").Filter(playwright.LocatorFilterOptions{HasText: "Change language"})
		check(t, expect.Locator(link).ToBeVisible())
	})
}

// Signing out ends the session: neither Back nor the dashboard's address
// shows the dashboard, or the key generated on it, again.
//
// This does not reach what Clear-Site-Data on the logout guards against, a
// dashboard restored from the back/forward cache: headless Chromium does not
// keep these no-store pages there, with the header or without it.
func TestSignOut(t *testing.T) {
	t.Parallel()
	// The pages after the logout belong to the OIDC mock.
	page := loggedIn(t, "logout", pageOptions{consoleErrors: true})
	click(t, page.Locator("form[action='/key/generate'] button"))
	secret := page.Locator("#new-key-val")
	check(t, expect.Locator(secret).ToHaveText(regexp.MustCompile(`^sk-`)))
	key, err := secret.TextContent()
	check(t, err)

	click(t, page.Locator(".account-trigger"))
	click(t, page.Locator("#account-menu button[type='submit']"))
	check(t, page.WaitForURL(regexp.MustCompile(`^http://localhost:8081/`)))

	signedOut := func(after string) {
		t.Helper()
		check(t, page.WaitForURL(regexp.MustCompile(`/oauth2/authorize`)))
		content, err := page.Content()
		check(t, err)
		if strings.Contains(content, key) {
			t.Errorf("%s shows the key again", after)
		}
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	signedOut("Back after signing out")
	goTo(t, page, seeded.URL+"/")
	signedOut("the dashboard after signing out")
}

// On a phone the header keeps to the screen: the avatar stays, the name goes,
// and on the narrowest screens the admin label too.
func TestPhoneHeader(t *testing.T) {
	t.Parallel()
	fits := func(t *testing.T, page playwright.Page) {
		t.Helper()
		over, err := page.Evaluate(`[document.documentElement, document.querySelector("header")]
			.some(el => el.scrollWidth > el.clientWidth)`)
		check(t, err)
		if over == true {
			t.Errorf("%s overflows the screen", page.URL())
		}
	}

	t.Run("long name", func(t *testing.T) {
		t.Parallel()
		page := loggedIn(t, "oezlem", pageOptions{viewport: &playwright.Size{Width: 375, Height: 800}})
		check(t, expect.Locator(page.Locator(".avatar")).ToHaveText("Ö"))
		check(t, expect.Locator(page.Locator(".account-name")).ToBeHidden())
		fits(t, page)
		click(t, page.Locator(".account-trigger"))
		check(t, expect.Locator(page.Locator("#account-menu")).ToContainText("Özlem Müller-Lüdenscheidt"))
	})
	t.Run("admin", func(t *testing.T) {
		t.Parallel()
		page := loggedIn(t, "admin", pageOptions{viewport: &playwright.Size{Width: 375, Height: 800}})
		goTo(t, page, "/admin")
		check(t, expect.Locator(page.Locator(".brand-page")).ToBeVisible())
		fits(t, page)
		check(t, page.SetViewportSize(320, 800))
		check(t, expect.Locator(page.Locator(".brand-page")).ToBeHidden())
		fits(t, page)
	})
}
