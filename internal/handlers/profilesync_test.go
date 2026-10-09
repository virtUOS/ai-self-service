package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
)

// getPage issues an authenticated GET the way a browser would.
func getPage(t *testing.T, ui *UI, h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// dashboardLang is the language getPage's requests render in.
func dashboardLang() i18n.Lang {
	return i18n.FromRequest(httptest.NewRequest(http.MethodGet, "/", nil))
}

// The dashboard only reads. Limits reach the gateway when a key is created
// and through the limit sync after an admin change; a page view pushing them
// once meant a failed profile lookup could push empty, unlimited, limits.
func TestDashboardNeverPushesLimits(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "psync-read")
	ctx := context.Background()
	post(t, ui, ui.GenerateKey, "/key/generate")

	// Move the user to a profile with different limits, as an admin would.
	p := &database.Profile{Name: "test quota"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProfileQuotas(ctx, p.ID, []database.ProfileQuota{
		{Budget: 0.01, Period: "1h"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProfile(ctx, user.ID, &p.ID); err != nil {
		t.Fatal(err)
	}

	before := len(fake.Relimited)
	if rec := getPage(t, ui, ui.Dashboard, "/"); rec.Code != http.StatusOK {
		t.Fatalf("dashboard returned %d", rec.Code)
	}
	if len(fake.Relimited) != before {
		t.Error("loading the dashboard pushed limits to the gateway")
	}
}

// A profile that cannot be loaded is reported, not papered over. The extend
// date and the model list would otherwise show server-wide defaults as if
// they were this user's.
func TestDashboardReportsAProfileItCannotLoad(t *testing.T) {
	ui, fake, store, _ := newTestUI(t, "psync-noprofile")
	ctx := context.Background()
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.AvailableModels = []string{"every-model-on-the-gateway"}

	// The user has no profile of their own, so without a default the lookup
	// fails.
	if err := store.ExecRaw(ctx, "UPDATE profiles SET is_default = 0"); err != nil {
		t.Fatal(err)
	}

	rec := getPage(t, ui, ui.Dashboard, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard returned %d", rec.Code)
	}
	body, lang := rec.Body.String(), dashboardLang()
	if !strings.Contains(body, i18n.T(lang, "dash.error.profile")) {
		t.Error("the page does not say the profile could not be loaded")
	}
	// Both need the profile and would fail; deleting does not.
	for _, action := range []string{`action="/key/extend"`, `action="/key/generate"`} {
		if strings.Contains(body, action) {
			t.Errorf("the page still offers %s", action)
		}
	}
	if !strings.Contains(body, `action="/key/delete"`) {
		t.Error("the page no longer offers to delete the key")
	}
	if strings.Contains(body, "every-model-on-the-gateway") {
		t.Error("the page lists the gateway's models as if the user could use them all")
	}
}

// A key that cannot be loaded must not read as "you have no key", and must not
// invite the user to generate one.
func TestDashboardReportsAKeyItCannotLoad(t *testing.T) {
	ui, _, store, _ := newTestUI(t, "psync-nokey")
	ctx := context.Background()
	post(t, ui, ui.GenerateKey, "/key/generate")

	if err := store.ExecRaw(ctx, "ALTER TABLE api_keys RENAME TO api_keys_gone"); err != nil {
		t.Fatal(err)
	}

	rec := getPage(t, ui, ui.Dashboard, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard returned %d", rec.Code)
	}
	body, lang := rec.Body.String(), dashboardLang()
	if !strings.Contains(body, i18n.T(lang, "dash.error.key")) {
		t.Error("the page does not say the key could not be loaded")
	}
	if strings.Contains(body, i18n.T(lang, "dash.nokey")) {
		t.Error("the page claims the user has no key")
	}
	if strings.Contains(body, i18n.T(lang, "dash.generate")) {
		t.Error("the page offers to generate a key")
	}
}

// A new key already carries its profile's limits, so the limit sync must not
// push them a second time.
func TestGeneratedKeyIsNotPendingForTheLimitSync(t *testing.T) {
	ui, _, store, _ := newTestUI(t, "psync-gen")
	post(t, ui, ui.GenerateKey, "/key/generate")

	st, err := store.GetLimitSyncStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 0 {
		t.Errorf("pending = %d after generating a key, want 0", st.Pending)
	}
}

// When the gateway cannot be read, the usage card says so and makes no claim
// it cannot back: neither "no usage limit" nor "no usage recorded".
func TestDashboardUsageCardAdmitsAFailedRead(t *testing.T) {
	ui, fake, _, _ := newTestUI(t, "psync-usagefail")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.UsageErr = errors.New("gateway down")

	body, lang := getPage(t, ui, ui.Dashboard, "/").Body.String(), dashboardLang()
	if !strings.Contains(body, i18n.T(lang, "dash.error.usage")) {
		t.Error("the usage card does not say the read failed")
	}
	for _, key := range []string{"dash.quota.unlimited", "dash.usagestats.none"} {
		if strings.Contains(body, i18n.T(lang, key)) {
			t.Errorf("the usage card claims %q although the read failed", i18n.T(lang, key))
		}
	}
}
