package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/virtuos/ai-self-service/internal/database"
)

// postToUser attaches userID as the chi URL param SetUserProfile reads, since
// httptest requests carry no route context on their own.
func postToUser(t *testing.T, h http.HandlerFunc, rec *httptest.ResponseRecorder, req *http.Request, userID int64) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(userID, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.ServeHTTP(rec, req)
}

// The form round-trips all three fields.
func TestSetUserProfileStoresTheDeadline(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "pexpf1")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-x", "x@uni-osnabrueck.de", "X")
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"profile_id":       {"0"},
		"expires_at":       {"2026-10-04"},
		"after_expiry":     {"0"},
		"revoke_at_expiry": {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	postToUser(t, a.SetUserProfile, rec, req, u.ID)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt == nil {
		t.Fatal("deadline not stored")
	}
	if y, m, d := got.ProfileExpiresAt.Date(); y != 2026 || m != time.October || d != 4 {
		t.Errorf("deadline = %v, want 2026-10-04", got.ProfileExpiresAt)
	}
	if !got.RevokeKeyAtExpiry {
		t.Error("revoke flag not stored")
	}
}

// An empty date means a permanent assignment, and must clear a deadline that
// was previously set — that is how an admin takes one off.
func TestSetUserProfileClearsTheDeadline(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "pexpf2")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-y", "y@uni-osnabrueck.de", "Y")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, nil, &tomorrow, nil, true); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"profile_id": {"0"}, "expires_at": {""}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	postToUser(t, a.SetUserProfile, httptest.NewRecorder(), req, u.ID)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt != nil {
		t.Errorf("deadline = %v, want nil after submitting an empty date", got.ProfileExpiresAt)
	}
	if got.RevokeKeyAtExpiry {
		t.Error("revoke flag survived the deadline being cleared")
	}
}

// A date that is not a date must be refused rather than silently ignored,
// or an admin would believe a deadline was set when none was.
func TestSetUserProfileRejectsAMalformedDate(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "pexpf3")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-z", "z@uni-osnabrueck.de", "Z")
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"profile_id": {"0"}, "expires_at": {"next tuesday"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	postToUser(t, a.SetUserProfile, rec, req, u.ID)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt != nil {
		t.Errorf("deadline = %v, want nil for an unparseable date", got.ProfileExpiresAt)
	}

	// An unstored deadline alone does not prove a refusal: silently dropping
	// the field would look identical. The flash is what tells the admin their
	// date was rejected rather than accepted, so assert on it.
	if !strings.Contains(rec.Header().Get("Location"), "flash=Enter+the+date") {
		t.Errorf("redirect = %q, want the malformed-date flash", rec.Header().Get("Location"))
	}
}

// A refused date must leave an existing deadline alone. Treating the refusal
// as an empty field would quietly make a temporary assignment permanent —
// the opposite of what the admin was trying to do.
func TestSetUserProfileRefusalKeepsAnExistingDeadline(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "pexpf4")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-w", "w@uni-osnabrueck.de", "W")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, nil, &tomorrow, nil, false); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"profile_id": {"0"}, "expires_at": {"04.10.2026"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	postToUser(t, a.SetUserProfile, httptest.NewRecorder(), req, u.ID)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt == nil {
		t.Error("a refused date cleared the deadline that was already set")
	}
}

// A limit that drops silently is a support ticket: while a deadline is set the
// dashboard has to say so.
func TestDashboardShowsTheDeadline(t *testing.T) {
	ui, _, store, user := newTestUI(t, "pexpd1")
	ctx := context.Background()

	p := &database.Profile{Name: "thesis project"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Relative to now, not a fixed date: a hardcoded one eventually falls into
	// the past and the test would silently start exercising the overdue
	// wording instead of the one it names.
	//
	// In UTC, because that is what the value becomes once it round-trips
	// through the database and what the page formats. Building it from local
	// time made this fail for the two hours a day when the two are on
	// different calendar dates — passing all afternoon and failing at
	// midnight, which is the worst way for a test to be wrong.
	deadline := time.Now().UTC().AddDate(0, 0, 16)
	if err := store.SetUserProfileUntil(ctx, user.ID, &p.ID, &deadline, nil, false); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if !strings.Contains(body, deadline.UTC().Format("2006-01-02")) {
		t.Error("the dashboard does not show the deadline")
	}
	// The test UI renders German, so assert on the German sentence: the two
	// wordings differ only in the verb ("gilt" vs "galt"), which is exactly
	// the distinction under test.
	if !strings.Contains(body, "Dieses Profil gilt bis zum") {
		t.Error("a future deadline did not use the present-tense wording")
	}
	if strings.Contains(body, "Dieses Profil galt bis zum") {
		t.Error("a future deadline used the overdue wording")
	}
}

// An admin can send the user to a named profile instead of the default. Saying
// "the standard limits return" would then be wrong, so the notice names it.
func TestDashboardNamesTheDestinationProfile(t *testing.T) {
	ui, _, store, user := newTestUI(t, "pexpd4")
	ctx := context.Background()

	from := &database.Profile{Name: "raised"}
	to := &database.Profile{Name: "restricted"}
	if err := store.CreateProfile(ctx, from); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(ctx, to); err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(0, 0, 10)
	if err := store.SetUserProfileUntil(ctx, user.ID, &from.ID, &future, &to.ID, false); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if !strings.Contains(body, "restricted") {
		t.Error("the notice does not name the profile the user moves to")
	}
	if strings.Contains(body, "Standardgrenzen") {
		t.Error("the notice promised the standard limits, but a profile was chosen")
	}
}

// The worst case to get wrong: the key is about to be deleted, and a notice
// promising the standard limits would say the opposite of what happens.
func TestDashboardSaysTheKeyWillBeDeleted(t *testing.T) {
	ui, _, store, user := newTestUI(t, "pexpd5")
	ctx := context.Background()

	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(0, 0, 10)
	if err := store.SetUserProfileUntil(ctx, user.ID, &p.ID, &future, nil, true); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if !strings.Contains(body, "gelöscht") {
		t.Error("the notice does not warn that the key will be deleted")
	}
	if strings.Contains(body, "Standardgrenzen") {
		t.Error("the notice promised the standard limits while the key is to be deleted")
	}
}

// A destination profile deleted after the fact leaves the row pointing at
// nothing. The user falls back to the default, so the notice must say that
// rather than naming a profile that no longer exists.
func TestDashboardFallsBackWhenTheDestinationIsGone(t *testing.T) {
	ui, _, store, user := newTestUI(t, "pexpd6")
	ctx := context.Background()

	from := &database.Profile{Name: "raised"}
	to := &database.Profile{Name: "doomed"}
	if err := store.CreateProfile(ctx, from); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(ctx, to); err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(0, 0, 10)
	if err := store.SetUserProfileUntil(ctx, user.ID, &from.ID, &future, &to.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProfile(ctx, to.ID); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if strings.Contains(body, "doomed") {
		t.Error("the notice named a profile that has been deleted")
	}
	if !strings.Contains(body, "Standardgrenzen") {
		t.Error("the notice did not fall back to the standard limits")
	}
}

// Between the deadline passing and the job running, the profile is still
// applied — so "applies until <a past date>" states something the page itself
// contradicts. The wording changes; the date does not.
func TestDashboardMarksAPassedDeadlineOverdue(t *testing.T) {
	ui, _, store, user := newTestUI(t, "pexpd3")
	ctx := context.Background()

	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	// UTC, matching what the page formats after the value round-trips through
	// the database. A local-time comparison passes or fails depending on the
	// hour of day.
	passed := time.Now().UTC().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, user.ID, &p.ID, &passed, nil, false); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if !strings.Contains(body, "Dieses Profil galt bis zum") {
		t.Error("a passed deadline did not use the overdue wording")
	}
	if strings.Contains(body, "Dieses Profil gilt bis zum") {
		t.Error("a passed deadline still claimed the profile applies until then")
	}
	// The date still has to be there: the user needs to know which deadline
	// this is, not just that one went by.
	if !strings.Contains(body, passed.UTC().Format("2006-01-02")) {
		t.Error("the overdue notice dropped the date")
	}
}

// A permanent assignment must say nothing, rather than showing an empty date.
//
// The dashboard's Extend button already renders the word "until" (and German
// "bis") unconditionally, so a bare substring check for those words would fail
// for the wrong reason. Assert on the exact deadline sentence fragment instead.
func TestDashboardSaysNothingWithoutADeadline(t *testing.T) {
	ui, _, store, user := newTestUI(t, "pexpd2")
	ctx := context.Background()

	p := &database.Profile{Name: "standard"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProfile(ctx, user.ID, &p.ID); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if strings.Contains(body, "This profile applies until") || strings.Contains(body, "Dieses Profil gilt bis") {
		t.Error("the dashboard mentions a deadline for a permanent assignment")
	}
}

// While a deadline has already passed, the expiry job owns the user's limits
// — it may be reverting them right now — so the dashboard must not push the
// profile it just read. Doing so would race the job: if the job wins by
// clearing the deadline right after the dashboard reads it, a push here would
// leave the old limits enforced with no deadline left to correct them.
func TestDashboardSkipsSyncWithAPassedDeadline(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "pexpd3")
	ctx := context.Background()

	post(t, ui, ui.GenerateKey, "/key/generate")
	k, err := store.GetAPIKeyByUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	p := &database.Profile{Name: "elevated"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProfileQuotas(ctx, p.ID, []database.ProfileQuota{
		{Budget: 0.01, Period: "1h"},
	}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, user.ID, &p.ID, &past, nil, false); err != nil {
		t.Fatal(err)
	}

	getPage(t, ui, ui.Dashboard, "/")

	if _, ok := fake.LimitsByRef[k.LiteLLMKey]; ok {
		t.Error("dashboard pushed limits for a user with an already-passed deadline")
	}
}
