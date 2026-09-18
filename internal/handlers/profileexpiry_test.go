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
	postToUser(t, a.SetUserProfile, httptest.NewRecorder(), req, u.ID)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt != nil {
		t.Errorf("deadline = %v, want nil for an unparseable date", got.ProfileExpiresAt)
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
	deadline := time.Date(2026, 10, 4, 23, 59, 59, 0, time.UTC)
	if err := store.SetUserProfileUntil(ctx, user.ID, &p.ID, &deadline, nil, false); err != nil {
		t.Fatal(err)
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()

	if !strings.Contains(body, "2026-10-04") {
		t.Error("the dashboard does not show the deadline")
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
