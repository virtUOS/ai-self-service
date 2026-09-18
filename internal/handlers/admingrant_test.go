package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/session"
)

// actingAdminEmail is who the test's session belongs to — the admin pressing
// the buttons.
const actingAdminEmail = "acting@uni-osnabrueck.de"

// newGrantTestAdmin builds an Admin whose session belongs to actingAdminEmail.
func newGrantTestAdmin(t *testing.T, name string) (*Admin, *database.Store, string) {
	t.Helper()
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })

	store := database.NewStore(db)
	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := store.GetOrCreateUser(ctx, "sub-acting", actingAdminEmail, "Acting Admin")
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewManager(store, time.Hour, false)
	token, err := sessions.Create(ctx, user.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	a := &Admin{cfg: &config.Config{}, store: store, sessions: sessions}
	return a, store, token
}

// Granting from the panel writes a grant and audits who did it.
func TestGrantAdminHandlerRecordsAndAudits(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "grant1")
	ctx := context.Background()

	form := url.Values{"email": {"new@uni-osnabrueck.de"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	a.GrantAdmin(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("status %d, want a redirect", rec.Code)
	}
	ok, err := store.IsAdminGranted(ctx, "", "new@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the grant was not recorded")
	}

	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Action != database.AuditAdminGranted {
		t.Errorf("audit = %+v, want an %s event", events, database.AuditAdminGranted)
	}
}

// An address that is not an address is refused rather than stored: a typo
// would otherwise sit in the table looking like an admin.
func TestGrantAdminHandlerRejectsAMalformedAddress(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "grant2")
	ctx := context.Background()

	form := url.Values{"email": {"not-an-address"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	a.GrantAdmin(httptest.NewRecorder(), req)

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Errorf("got %d grants, want the malformed address refused", len(grants))
	}
}

// The foot-gun: an admin removing their own rights would have to be let back
// in by editing the deployment.
func TestRevokeAdminHandlerRefusesSelfRevoke(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "grant3")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, actingAdminEmail, "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"email": {actingAdminEmail}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	a.RevokeAdmin(httptest.NewRecorder(), req)

	ok, err := store.IsAdminGranted(ctx, "", actingAdminEmail)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("an admin revoked their own rights")
	}
}

// An address in ADMIN_IDS keeps its rights whatever the table holds, so a
// revoke against one must be refused rather than silently deleting a row and
// auditing a revoke that did not take effect.
func TestRevokeAdminHandlerRefusesAConfigEntry(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "grant5")
	ctx := context.Background()
	a.cfg = &config.Config{AdminIDs: []string{"fixed@uni-osnabrueck.de"}}

	form := url.Values{"email": {"fixed@uni-osnabrueck.de"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	a.RevokeAdmin(httptest.NewRecorder(), req)

	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Action == database.AuditAdminRevoked {
			t.Error("a refused revoke of a config entry was audited as a revoke")
		}
	}
}

// Revoking someone else works and is audited.
func TestRevokeAdminHandlerRemovesAndAudits(t *testing.T) {
	a, store, token := newGrantTestAdmin(t, "grant4")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, "victim@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"email": {"victim@uni-osnabrueck.de"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	a.RevokeAdmin(httptest.NewRecorder(), req)

	ok, err := store.IsAdminGranted(ctx, "", "victim@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("the grant survived a revoke")
	}
	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Action != database.AuditAdminRevoked {
		t.Errorf("audit = %+v, want an %s event", events, database.AuditAdminRevoked)
	}
}

// Config-derived admins must render without a remove button: the button would
// not work, since the env list outranks the table.
func TestAdminRowsMarkConfigEntriesUnremovable(t *testing.T) {
	cfg := &config.Config{AdminIDs: []string{"fixed@uni-osnabrueck.de"}}
	grants := []database.AdminGrant{{Email: "granted@uni-osnabrueck.de"}}

	rows := adminRows(cfg, grants, actingAdminEmail)

	var fixed, granted *adminRow
	for i := range rows {
		switch rows[i].Email {
		case "fixed@uni-osnabrueck.de":
			fixed = &rows[i]
		case "granted@uni-osnabrueck.de":
			granted = &rows[i]
		}
	}
	if fixed == nil || granted == nil {
		t.Fatalf("rows = %+v, want both the config entry and the grant", rows)
	}
	if fixed.Removable {
		t.Error("a config entry was marked removable")
	}
	if !granted.Removable {
		t.Error("a grant was not marked removable")
	}
}

// The acting admin's own row must not offer a remove button either.
func TestAdminRowsMarkSelf(t *testing.T) {
	grants := []database.AdminGrant{{Email: actingAdminEmail}}

	rows := adminRows(&config.Config{}, grants, actingAdminEmail)

	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if !rows[0].IsSelf {
		t.Error("the acting admin's row was not marked as self")
	}
	if rows[0].Removable {
		t.Error("the acting admin's own row offered a remove button")
	}
}
