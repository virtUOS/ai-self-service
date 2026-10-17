//go:build browser

package browsertest

import (
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// Every page every persona can reach renders without a script error.
func TestPagesHaveNoScriptErrors(t *testing.T) {
	t.Parallel()
	admins := map[string]bool{"admin": true, "admin2": true}
	for _, u := range seeded.Users {
		t.Run(u.Sub, func(t *testing.T) {
			t.Parallel()
			page := loggedIn(t, u.Sub, pageOptions{})
			goTo(t, page, "/account")
			goTo(t, page, "/privacy")
			if !admins[u.Sub] {
				return
			}
			for _, tab := range []string{"profiles", "users", "admins", "audit"} {
				goTo(t, page, "/admin#"+tab)
				check(t, expect.Locator(page.Locator("#tab-"+tab)).ToBeVisible())
			}
		})
	}
}

// Each persona's dashboard shows the state it was seeded into.
func TestDashboardStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sub   string
		check func(t *testing.T, page playwright.Page)
	}{
		{"student", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator(".quota-bar")).ToHaveCount(1))
			check(t, expect.Locator(page.Locator(".usage-chart")).ToBeVisible())
			rows := page.Locator("table.usage-models tbody tr")
			check(t, expect.Locator(rows).ToHaveCount(2))
			check(t, expect.Locator(rows).ToContainText([]string{"mock-small", "mock-large"}))
			// The client-side badge: a fresh key is far from expiring.
			check(t, expect.Locator(page.Locator(".expiry.badge-ok")).ToHaveText(regexp.MustCompile(`^\d+d left$`)))
		}},
		{"heavy", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator(".quota-fill.quota-full")).ToHaveCount(1))
			check(t, expect.Locator(page.Locator(".reset-at")).ToHaveText(regexp.MustCompile(`^(in \d+ (minute|minutes|hour|hours)|now)$`)))
		}},
		{"stacked", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator(".quota-bar")).ToHaveCount(3))
		}},
		{"restricted", func(t *testing.T, page playwright.Page) {
			models := page.Locator(".model-copy")
			check(t, expect.Locator(models).ToHaveCount(1))
			check(t, expect.Locator(models).ToHaveAttribute("data-model", "mock-small"))
		}},
		{"expiring", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator(".alert-warning")).ToContainText(regexp.MustCompile(`\d+ d left`)))
			check(t, expect.Locator(page.Locator(".expiry.badge-warn")).ToBeVisible())
		}},
		{"expired", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator(".alert-danger")).ToContainText("Your key has expired."))
			check(t, expect.Locator(page.Locator(".expiry.badge-expired")).ToHaveText("expired"))
		}},
		{"missing", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator(".alert-warning")).ToContainText("no longer recognises your API key"))
			// Nothing to report on a key the gateway does not have.
			check(t, expect.Locator(page.Locator(".quota-bar")).ToHaveCount(0))
			check(t, expect.Locator(page.Locator("form[action='/key/extend']")).ToHaveCount(0))
		}},
		{"nokey", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.GetByText("You don't have an API key yet.")).ToBeVisible())
			check(t, expect.Locator(page.Locator("form[action='/key/generate'] button")).ToBeEnabled())
		}},
		{"guest", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.GetByText("Your key is deleted afterwards")).ToBeVisible())
		}},
		{"intern", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.GetByText(`You then move to the "Tight" profile.`)).ToBeVisible())
		}},
		{"lapsed", func(t *testing.T, page playwright.Page) {
			check(t, expect.Locator(page.Locator("dl.info-grid").First()).ToContainText("default"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.sub, func(t *testing.T) {
			t.Parallel()
			tc.check(t, loggedIn(t, tc.sub, pageOptions{}))
		})
	}
}

// The whole life of a key in the browser: generated and shown once, copied,
// a regeneration declined and then confirmed, and deleted.
func TestKeyFlow(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "keyflow", pageOptions{clipboard: true})
	var confirm atomic.Bool
	page.OnDialog(func(d playwright.Dialog) {
		if confirm.Load() {
			_ = d.Accept()
		} else {
			_ = d.Dismiss()
		}
	})

	click(t, page.Locator("form[action='/key/generate'] button"))
	secret := page.Locator("#new-key-val")
	check(t, expect.Locator(secret).ToHaveText(regexp.MustCompile(`^sk-`)))
	first, err := secret.TextContent()
	check(t, err)
	// The one-time token that brought the key is gone from the address bar.
	check(t, expect.Page(page).ToHaveURL(seeded.URL+"/"))

	copyBtn := page.Locator("#new-key-val + .copy-btn")
	click(t, copyBtn)
	check(t, expect.Locator(copyBtn).ToHaveText("Copied!"))
	clip, err := page.Evaluate("navigator.clipboard.readText()")
	check(t, err)
	if clip != first {
		t.Errorf("clipboard = %q, want the new key", clip)
	}

	// Declining the confirmation sends nothing and leaves the button usable.
	regenerate := page.Locator("form[action='/key/generate'] button")
	click(t, regenerate)
	check(t, expect.Locator(regenerate).ToBeEnabled())
	check(t, expect.Locator(secret).ToHaveText(first))

	confirm.Store(true)
	click(t, regenerate)
	check(t, expect.Locator(secret).Not().ToHaveText(first))
	check(t, expect.Locator(secret).ToHaveText(regexp.MustCompile(`^sk-`)))
	check(t, expect.Locator(page.Locator(".key-box").First()).ToBeVisible())

	click(t, page.Locator("form[action='/key/delete'] button"))
	check(t, expect.Locator(page.GetByText("You don't have an API key yet.")).ToBeVisible())
}

// The example switches between curl and Python and keeps the choice.
func TestExampleSwitcherPersists(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "student", pageOptions{})
	code := page.Locator("#curl-code")
	check(t, expect.Locator(code).ToContainText("curl "))

	python := page.Locator(".example-tabs button[data-lang='python']")
	click(t, python)
	check(t, expect.Locator(code).ToContainText("from openai import OpenAI"))

	if _, err := page.Reload(); err != nil {
		t.Fatal(err)
	}
	check(t, expect.Locator(code).ToContainText("from openai import OpenAI"))
	check(t, expect.Locator(python).ToHaveAttribute("aria-pressed", "true"))
}

// A model chip copies the bare name and switches the example to it.
func TestModelChipCopiesName(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "student", pageOptions{clipboard: true})
	chip := page.Locator(".model-copy[data-model='mock-large']")
	click(t, chip)
	check(t, expect.Locator(chip).ToHaveText("Copied!"))
	check(t, expect.Locator(page.Locator("#curl-model")).ToHaveText("mock-large"))
	clip, err := page.Evaluate("navigator.clipboard.readText()")
	check(t, err)
	if clip != "mock-large" {
		t.Errorf("clipboard = %q, want mock-large", clip)
	}
	// The label comes back.
	check(t, expect.Locator(chip).ToHaveText("mock-large"))
}

// A dashboard whose session ends goes to the login on its own.
func TestSessionExpiryRedirectsToLogin(t *testing.T) {
	t.Parallel()
	// The status poll's 401 is logged as a failed request.
	page := loggedIn(t, "nokey", pageOptions{consoleErrors: true})
	if err := page.Context().ClearCookies(); err != nil {
		t.Fatal(err)
	}
	err := page.WaitForURL(regexp.MustCompile(`/oauth2/authorize`), playwright.PageWaitForURLOptions{
		Timeout: playwright.Float(15000),
	})
	if err != nil {
		t.Fatalf("still at %s after the session ended: %v", page.URL(), err)
	}
}

// Help buttons show their text on hover and on focus, and Escape hides it.
func TestHelpTooltip(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "student", pageOptions{})
	help := page.Locator("button.help").First()
	text, err := help.GetAttribute("data-help")
	check(t, err)
	popover := page.Locator(".help-popover")

	check(t, help.Hover())
	check(t, expect.Locator(popover).ToBeVisible())
	check(t, expect.Locator(popover).ToHaveText(strings.TrimSpace(text)))
	check(t, page.Mouse().Move(0, 0))
	check(t, expect.Locator(popover).ToBeHidden())

	check(t, help.Focus())
	check(t, expect.Locator(popover).ToBeVisible())
	check(t, page.Keyboard().Press("Escape"))
	check(t, expect.Locator(popover).ToBeHidden())
}
