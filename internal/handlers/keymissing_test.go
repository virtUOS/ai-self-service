package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/internal/keyprovider"
	"github.com/virtuos/keyosk/internal/metrics"
	"github.com/virtuos/keyosk/internal/metrics/metricstest"
)

// missingKeyCount reads the counter of keys marked as missing for one source.
func missingKeyCount(t *testing.T, source string) float64 {
	t.Helper()
	return metricstest.Counter(t, "keyosk_keys_marked_missing_total", `source="`+source+`"`)
}

var errGone = fmt.Errorf("LiteLLM /key/info returned 404: %w", keyprovider.ErrKeyNotFound)

// missingKeyAudits counts the key.missing entries in the audit log.
func missingKeyAudits(t *testing.T, store *database.Store) int {
	t.Helper()
	events, err := store.ListAuditEvents(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Action == database.AuditKeyMissing {
			n++
		}
	}
	return n
}

// storedKey returns the user's key record, which must exist.
func storedKey(t *testing.T, store *database.Store, user *database.User) *database.APIKey {
	t.Helper()
	k, err := store.GetAPIKeyByUser(context.Background(), user.ID)
	if err != nil || k == nil {
		t.Fatalf("key = %+v, err = %v; want the record kept", k, err)
	}
	return k
}

// A key the gateway does not have is marked, not dropped (issue #108): the
// gateway may only be wrong for now. The dashboard says the key does not work
// and offers a new one, but not an extension, and a second view neither marks
// nor counts it again.
func TestDashboardMarksAKeyTheGatewayNoLongerHas(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-dash")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.WindowsErr = errGone
	before := missingKeyCount(t, metrics.SourceDashboard)

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()
	if !strings.Contains(body, i18n.T(dashboardLang(), "dash.key.missing")) {
		t.Error("dashboard does not say the gateway lacks the key")
	}
	if !strings.Contains(body, `action="/key/generate"`) {
		t.Error("dashboard offers no new key")
	}
	if strings.Contains(body, `action="/key/extend"`) {
		t.Error("dashboard offers to extend a key the gateway does not have")
	}
	if k := storedKey(t, store, user); k.MissingSince == nil {
		t.Error("key is not marked")
	}
	if n := missingKeyAudits(t, store); n != 1 {
		t.Errorf("key.missing audit entries = %d, want 1", n)
	}
	if got := missingKeyCount(t, metrics.SourceDashboard) - before; got != 1 {
		t.Errorf("keys_missing{source=dashboard} rose by %v, want 1", got)
	}

	// The marker is stored, so the page no longer asks the gateway about the
	// key, however it would answer.
	fake.WindowsErr = nil
	body = getPage(t, ui, ui.Dashboard, "/").Body.String()
	if !strings.Contains(body, i18n.T(dashboardLang(), "dash.key.missing")) {
		t.Error("second view does not say the gateway lacks the key")
	}
	if got := missingKeyCount(t, metrics.SourceDashboard) - before; got != 1 {
		t.Errorf("keys_missing{source=dashboard} rose by %v over two views, want 1", got)
	}
	if n := missingKeyAudits(t, store); n != 1 {
		t.Errorf("key.missing audit entries after two views = %d, want 1", n)
	}
}

// Any other read failure is not a missing key: the key may well work, so it is
// not marked and the card only says the figures are incomplete.
func TestDashboardDoesNotMarkAKeyOnOtherErrors(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-other")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.WindowsErr = errors.New("gateway down")

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()
	if strings.Contains(body, i18n.T(dashboardLang(), "dash.key.missing")) {
		t.Error("dashboard calls a key missing on a plain read failure")
	}
	if k := storedKey(t, store, user); k.MissingSince != nil {
		t.Errorf("key marked missing on a plain read failure: %v", k.MissingSince)
	}
}

// Extending a key the gateway does not have marks it and sends the user back
// to the dashboard, which explains it.
func TestExtendMarksAKeyTheGatewayNoLongerHas(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-extend")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.ExpiryErr = fmt.Errorf("LiteLLM /key/update returned 404: %w", keyprovider.ErrKeyNotFound)
	before := missingKeyCount(t, metrics.SourceExtend)

	rec := post(t, ui, ui.ExtendKey, "/key/extend")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("extend answered %d to %q, want a redirect to /", rec.Code, rec.Header().Get("Location"))
	}
	if k := storedKey(t, store, user); k.MissingSince == nil {
		t.Error("key is not marked")
	}
	if n := missingKeyAudits(t, store); n != 1 {
		t.Errorf("key.missing audit entries = %d, want 1", n)
	}
	if got := missingKeyCount(t, metrics.SourceExtend) - before; got != 1 {
		t.Errorf("keys_missing{source=extend} rose by %v, want 1", got)
	}
	fake.WindowsErr = errGone
	body := getPage(t, ui, ui.Dashboard, "/").Body.String()
	if !strings.Contains(body, i18n.T(dashboardLang(), "dash.key.missing")) {
		t.Error("dashboard after the redirect does not say the gateway lacks the key")
	}
}

// A marked key is not extended, even by a request the dashboard would not have
// sent: there is nothing in the gateway to extend. The gateway is not asked.
func TestExtendRefusesAMarkedKey(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-extend-marked")
	post(t, ui, ui.GenerateKey, "/key/generate")
	k := storedKey(t, store, user)
	if _, err := store.MarkKeyMissing(context.Background(), k.ID, user.ID, user.Email, "test"); err != nil {
		t.Fatal(err)
	}
	fake.ExpiryErr = errors.New("the gateway must not be asked")
	expiry := k.ExpiresAt

	rec := post(t, ui, ui.ExtendKey, "/key/extend")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("extend answered %d to %q, want a redirect to /", rec.Code, rec.Header().Get("Location"))
	}
	if got := storedKey(t, store, user).ExpiresAt; !got.Equal(expiry) {
		t.Errorf("expiry moved from %v to %v", expiry, got)
	}
	if len(fake.Extended) != 0 {
		t.Errorf("gateway extended %v", fake.Extended)
	}
}

// A marked key can be replaced: the new key starts unmarked, and the old
// one, which the gateway no longer has, is not an error.
func TestGenerateReplacesAMarkedKey(t *testing.T) {
	ui, _, store, user := newTestUI(t, "missing-regenerate")
	post(t, ui, ui.GenerateKey, "/key/generate")
	old := storedKey(t, store, user)
	if _, err := store.MarkKeyMissing(context.Background(), old.ID, user.ID, user.Email, "test"); err != nil {
		t.Fatal(err)
	}

	rec := post(t, ui, ui.GenerateKey, "/key/generate")
	if rec.Code != http.StatusFound {
		t.Fatalf("generate answered %d, want a redirect", rec.Code)
	}
	k := storedKey(t, store, user)
	if k.KeyRef == old.KeyRef || k.MissingSince != nil {
		t.Errorf("key = %+v, want a new, unmarked key", k)
	}
}

// The owner can delete a marked key, which is what a key the gateway lacks is
// treated as: already gone.
func TestDeleteRemovesAMarkedKey(t *testing.T) {
	ui, _, store, user := newTestUI(t, "missing-delete")
	post(t, ui, ui.GenerateKey, "/key/generate")
	k := storedKey(t, store, user)
	if _, err := store.MarkKeyMissing(context.Background(), k.ID, user.ID, user.Email, "test"); err != nil {
		t.Fatal(err)
	}

	post(t, ui, ui.DeleteKey, "/key/delete")
	if got, err := store.GetAPIKeyByUser(context.Background(), user.ID); err != nil || got != nil {
		t.Errorf("key = %+v, err = %v; want it removed", got, err)
	}
}

// The admin panel shows which keys are marked, and since when.
func TestPanelShowsAMarkedKey(t *testing.T) {
	a, store, token, _ := newSyncTestAdmin(t, "missing-panel")
	ctx := context.Background()
	user, err := store.GetOrCreateUser(ctx, "sub-holder", "holder@uni-osnabrueck.de", "Holder")
	if err != nil {
		t.Fatal(err)
	}
	k := &database.APIKey{UserID: user.ID, KeyRef: "ref", KeyPrefix: "sk-holder", ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := store.ReplaceAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}

	body, lang := renderPanel(t, a, token)
	label := i18n.T(lang, "admin.key.missing")
	if strings.Contains(body, label) {
		t.Fatal("panel marks a key the gateway has")
	}

	stored, err := store.GetAPIKeyByUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkKeyMissing(ctx, stored.ID, user.ID, user.Email, "test"); err != nil {
		t.Fatal(err)
	}
	body, _ = renderPanel(t, a, token)
	// Dated in UTC or local time, depending on how the driver returns it.
	utc, local := time.Now().UTC().Format("2006-01-02"), time.Now().Format("2006-01-02")
	if !strings.Contains(body, label+" "+utc) && !strings.Contains(body, label+" "+local) {
		t.Errorf("panel lacks %q for the marked key", label+" "+utc)
	}
}
