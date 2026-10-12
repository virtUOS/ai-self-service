package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

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

// A key deleted in the gateway cannot be restored. The dashboard drops the
// record, says what happened, and offers a new key instead of showing one
// that does not work.
func TestDashboardRemovesAKeyTheGatewayNoLongerHas(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-dash")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.WindowsErr = errGone

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()
	if !strings.Contains(body, i18n.T(dashboardLang(), "dash.key.gone")) {
		t.Error("dashboard does not say the key was deleted")
	}
	if !strings.Contains(body, `action="/key/generate"`) {
		t.Error("dashboard offers no new key")
	}
	if k, err := store.GetAPIKeyByUser(context.Background(), user.ID); err != nil || k != nil {
		t.Errorf("key = %+v, err = %v; want the record removed", k, err)
	}
	if n := missingKeyAudits(t, store); n != 1 {
		t.Errorf("key.missing audit entries = %d, want 1", n)
	}
}

// Any other read failure is not a missing key: the key may well work, so the
// record stays and the card only says the figures are incomplete.
func TestDashboardKeepsTheKeyOnOtherErrors(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-other")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.WindowsErr = errors.New("gateway down")

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()
	if strings.Contains(body, i18n.T(dashboardLang(), "dash.key.gone")) {
		t.Error("dashboard calls a key deleted on a plain read failure")
	}
	if k, err := store.GetAPIKeyByUser(context.Background(), user.ID); err != nil || k == nil {
		t.Errorf("key = %+v, err = %v; want it kept", k, err)
	}
}

// Extending a key the gateway no longer has removes the record and sends the
// user back to the dashboard, which explains it.
func TestExtendRemovesAKeyTheGatewayNoLongerHas(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "missing-extend")
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.ExpiryErr = fmt.Errorf("LiteLLM /key/update returned 404: %w", keyprovider.ErrKeyNotFound)

	rec := post(t, ui, ui.ExtendKey, "/key/extend")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?gone=1" {
		t.Fatalf("extend answered %d to %q, want a redirect to /?gone=1", rec.Code, rec.Header().Get("Location"))
	}
	if k, err := store.GetAPIKeyByUser(context.Background(), user.ID); err != nil || k != nil {
		t.Errorf("key = %+v, err = %v; want the record removed", k, err)
	}
	if n := missingKeyAudits(t, store); n != 1 {
		t.Errorf("key.missing audit entries = %d, want 1", n)
	}
	body := getPage(t, ui, ui.Dashboard, "/?gone=1").Body.String()
	if !strings.Contains(body, i18n.T(dashboardLang(), "dash.key.gone")) {
		t.Error("dashboard after the redirect does not say the key was deleted")
	}
}
