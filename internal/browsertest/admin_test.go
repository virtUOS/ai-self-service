//go:build browser

package browsertest

import (
	"fmt"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// profileRow is the profiles table's row for a profile.
func profileRow(page playwright.Page, name string) playwright.Locator {
	return page.Locator("#tab-profiles tbody tr").Filter(playwright.LocatorFilterOptions{
		Has: page.Locator(fmt.Sprintf("strong:text-is(%q)", name)),
	})
}

// userRow is the users table's row for a subject.
func userRow(page playwright.Page, sub string) playwright.Locator {
	return page.Locator("#tab-users tbody tr").Filter(playwright.LocatorFilterOptions{
		Has: page.Locator(fmt.Sprintf("td:text-is(%q)", sub)),
	})
}

// adminView is tall enough to show the whole users table. A row menu closes
// when the page scrolls, and Playwright scrolls a row into view before it
// clicks the menu button, which would close the menu again at once.
var adminView = pageOptions{viewport: &playwright.Size{Width: 1280, Height: 2400}}

// rowAction opens a row's ⋮ menu and clicks one of its entries.
func rowAction(t *testing.T, row playwright.Locator, entry string) {
	t.Helper()
	click(t, row.Locator(".row-menu-btn"))
	click(t, row.Locator(".row-menu").GetByRole("button", playwright.LocatorGetByRoleOptions{
		Name: entry, Exact: playwright.Bool(true),
	}))
}

// The selected tab follows the URL hash, and a click writes it back.
func TestAdminTabsFollowHash(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "admin", adminView)
	goTo(t, page, "/admin#audit")
	check(t, expect.Locator(page.Locator("#tab-audit")).ToBeVisible())
	check(t, expect.Locator(page.Locator("#tab-profiles")).ToBeHidden())

	click(t, page.Locator(".tab-btn[data-tab='users']"))
	check(t, expect.Page(page).ToHaveURL(seeded.URL+"/admin#users"))
	check(t, expect.Locator(page.Locator("#tab-users")).ToBeVisible())

	// "admin" is an alias for the admins tab.
	goTo(t, page, "/admin#admin")
	check(t, expect.Locator(page.Locator("#tab-admins")).ToBeVisible())
	// The audit log has the seed's work in it, the lapsed deadline included.
	goTo(t, page, "/admin#audit")
	check(t, expect.Locator(page.Locator("#tab-audit")).ToContainText("user.profile_expired"))
}

// The edit dialog opens filled with the profile's values.
func TestProfileDialogPrefill(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "admin", adminView)
	goTo(t, page, "/admin")
	dialog := page.Locator("#profile-dialog")
	cancel := dialog.GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Cancel"})

	rowAction(t, profileRow(page, "Stacked"), "Edit")
	check(t, expect.Locator(dialog).ToBeVisible())
	check(t, expect.Locator(page.Locator("#pf-name")).ToHaveValue("Stacked"))
	check(t, expect.Locator(page.Locator("#pf-quotas .quota-row")).ToHaveCount(3))
	periods, err := page.Locator("#pf-quotas select").EvaluateAll("els => els.map(e => e.value).join(' ')")
	check(t, err)
	if periods != "1h 24h 7d" {
		t.Errorf("quota periods = %v, want 1h 24h 7d", periods)
	}
	check(t, expect.Locator(page.Locator("#pf-default")).ToBeEnabled())
	click(t, cancel)
	check(t, expect.Locator(dialog).ToBeHidden())

	rowAction(t, profileRow(page, "Small only"), "Edit")
	check(t, expect.Locator(page.Locator("#pf-model-picker input[value='mock-small']")).ToBeChecked())
	check(t, expect.Locator(page.Locator("#pf-model-picker input[value='mock-large']")).Not().ToBeChecked())
	check(t, expect.Locator(page.Locator("#pf-tpm")).ToHaveValue("1000"))
	click(t, cancel)

	// The default profile cannot stop being the default.
	rowAction(t, profileRow(page, "default"), "Edit")
	check(t, expect.Locator(page.Locator("#pf-default")).ToBeChecked())
	check(t, expect.Locator(page.Locator("#pf-default")).ToBeDisabled())
	click(t, cancel)

	// A new profile starts empty after an edit.
	click(t, page.Locator("button[onclick='openNewProfile()']"))
	check(t, expect.Locator(page.Locator("#pf-name")).ToHaveValue(""))
	check(t, expect.Locator(page.Locator("#pf-quotas .quota-row")).ToHaveCount(0))
	check(t, expect.Locator(page.Locator("#pf-default")).ToBeEnabled())
}

// Quota rows added and removed in the dialog are what the form posts.
func TestProfileEdit(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "admin", adminView)
	goTo(t, page, "/admin")
	rowAction(t, profileRow(page, "Scratch"), "Edit")

	add := page.Locator("#profile-dialog").GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Add window"})
	rows := page.Locator("#pf-quotas .quota-row")
	click(t, add)
	click(t, add)
	check(t, expect.Locator(rows).ToHaveCount(2))
	check(t, rows.Nth(0).Locator("input").Fill("4"))
	_, err := rows.Nth(0).Locator("select").SelectOption(playwright.SelectOptionValues{Values: &[]string{"7d"}})
	check(t, err)
	check(t, rows.Nth(1).Locator("input").Fill("99"))
	click(t, rows.Nth(1).GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Delete"}))
	check(t, expect.Locator(rows).ToHaveCount(1))

	click(t, page.Locator("#profile-submit-btn"))
	check(t, expect.Locator(page.Locator(".alert").First()).ToContainText("Profile updated"))
	check(t, expect.Locator(profileRow(page, "Scratch")).ToContainText("4.00"))
	check(t, expect.Locator(profileRow(page, "Scratch")).Not().ToContainText("99"))
}

// Deleting a profile asks first.
func TestProfileDelete(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "admin", adminView)
	goTo(t, page, "/admin")
	page.OnDialog(func(d playwright.Dialog) { _ = d.Accept() })
	rowAction(t, profileRow(page, "Unused"), "Delete")
	check(t, expect.Locator(page.Locator(".alert").First()).ToContainText("Profile deleted"))
	check(t, expect.Locator(profileRow(page, "Unused")).ToHaveCount(0))
}

// The user dialog shows what happens at a deadline only once there is one.
func TestUserDialogExpiryToggle(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "admin", adminView)
	goTo(t, page, "/admin#users")
	rowAction(t, userRow(page, "nokey"), "Edit")

	dialog := page.Locator("dialog[open]")
	check(t, expect.Locator(dialog).ToBeVisible())
	options := dialog.Locator(".expiry-options")
	check(t, expect.Locator(options).ToBeHidden())
	date := dialog.Locator("input[type='date']")
	check(t, date.Fill("2099-01-01"))
	check(t, expect.Locator(options).ToBeVisible())
	check(t, date.Fill(""))
	check(t, expect.Locator(options).ToBeHidden())

	check(t, page.Keyboard().Press("Escape"))
	check(t, expect.Locator(dialog).ToHaveCount(0))
}

// A user's dialog opened for an assignment with a deadline shows it.
func TestUserDialogShowsDeadline(t *testing.T) {
	t.Parallel()
	page := loggedIn(t, "admin", adminView)
	goTo(t, page, "/admin#users")
	check(t, expect.Locator(userRow(page, "intern")).ToContainText("then Tight"))
	rowAction(t, userRow(page, "intern"), "Edit")
	dialog := page.Locator("dialog[open]")
	check(t, expect.Locator(dialog.Locator(".expiry-options")).ToBeVisible())
	check(t, expect.Locator(dialog.Locator("select[name='after_expiry'] option:checked")).ToHaveText("Tight"))
}
