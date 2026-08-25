package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/virtuos/ai-self-service/internal/database"
)

// Changing a profile must reach keys that already exist. Limits were applied
// only at creation, so a profile edit — or moving a user between profiles —
// left the old key unrestricted while the dashboard advertised the new limit.
func TestDashboardReappliesProfileLimits(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "psync1")
	ctx := context.Background()

	// A key issued with no quota.
	post(t, ui, ui.GenerateKey, "/key/generate")
	k, _ := store.GetAPIKeyByUser(ctx, user.ID)

	// The user is then put on a profile that does have one.
	p := &database.Profile{Name: "test quota", QuotaTokens: 10_000, QuotaPeriod: "1h"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProfile(ctx, user.ID, &p.ID); err != nil {
		t.Fatal(err)
	}

	getPage(t, ui, ui.Dashboard, "/")

	got, ok := fake.LimitsByRef[k.LiteLLMKey]
	if !ok {
		t.Fatal("dashboard did not push limits to the existing key")
	}
	if got.QuotaTokens != 10_000 || got.QuotaPeriod != "1h" {
		t.Errorf("pushed %+v, want 10000/1h", got)
	}
}

// A failed sync must not break the dashboard: the page still has to render.
func TestDashboardSurvivesFailedLimitSync(t *testing.T) {
	ui, fake, _, _ := newTestUI(t, "psync2")
	post(t, ui, ui.GenerateKey, "/key/generate")

	fake.LimitsErr = errors.New("gateway down")
	rec := getPage(t, ui, ui.Dashboard, "/")
	if rec.Code != http.StatusOK {
		t.Errorf("dashboard returned %d when the limit sync failed", rec.Code)
	}
}

// getPage issues an authenticated GET the way a browser would.
func getPage(t *testing.T, ui *UI, h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}
