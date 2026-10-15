package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/session"
)

// actingAdminEmail is who the test's session belongs to: the admin pressing
// the buttons.
const actingAdminEmail = "acting@uni-osnabrueck.de"

// newAuthTestStore opens a migrated in-memory store.
func newAuthTestStore(t *testing.T, name string) *database.Store {
	t.Helper()
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })

	store := database.NewStore(db)
	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

// newTestAdmin builds an Admin whose session belongs to the returned account,
// actingAdminEmail. The account holds no rights yet: the handlers are called
// directly, so only tests of the gate need the flag.
func newTestAdmin(t *testing.T, name string) (*Admin, *database.Store, string, *database.User) {
	t.Helper()
	store := newAuthTestStore(t, name)
	ctx := context.Background()
	actor, err := store.GetOrCreateUser(ctx, "sub-acting", actingAdminEmail, "Acting Admin")
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewManager(store, time.Hour, false)
	token, err := sessions.Create(ctx, actor.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return &Admin{cfg: &config.Config{}, store: store, sessions: sessions}, store, token, actor
}

// gateStatus runs the admin middleware for the session and reports the status.
func gateStatus(a *Admin, token string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code
}

// postAccount posts an account id to one of the Admins tab's handlers.
func postAccount(h http.HandlerFunc, token, path string, userID int64) *httptest.ResponseRecorder {
	form := url.Values{"user_id": {strconv.FormatInt(userID, 10)}}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func isAdmin(t *testing.T, store *database.Store, id int64) bool {
	t.Helper()
	u, err := store.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u.IsAdmin
}

func lastAudit(t *testing.T, store *database.Store) *database.AuditEvent {
	t.Helper()
	events, err := store.ListAuditEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		return nil
	}
	return &events[0]
}

// The flag on the account is what opens the panel, with nothing in the
// deployment naming the person.
func TestAdminGateAdmitsAFlaggedAccount(t *testing.T) {
	a, store, token, actor := newTestAdmin(t, "acct-gate1")
	if err := store.SetUserAdmin(context.Background(), actor.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := gateStatus(a, token); got != http.StatusOK {
		t.Errorf("status %d for a flagged account, want 200", got)
	}
}

// Nobody is an admin until flagged, whatever their address or token says.
func TestAdminGateRefusesAnUnflaggedAccount(t *testing.T) {
	a, _, token, _ := newTestAdmin(t, "acct-gate2")
	if got := gateStatus(a, token); got != http.StatusForbidden {
		t.Errorf("status %d for an unflagged account, want 403", got)
	}
}

// Rights are read on every request, so a removal does not wait for the
// person's session to end.
func TestAdminRemovalTakesEffectOnTheNextRequest(t *testing.T) {
	a, store, token, actor := newTestAdmin(t, "acct-gate3")
	ctx := context.Background()
	if err := store.SetUserAdmin(ctx, actor.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := gateStatus(a, token); got != http.StatusOK {
		t.Fatalf("status %d before the removal, want 200", got)
	}
	if err := store.SetUserAdmin(ctx, actor.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := gateStatus(a, token); got != http.StatusForbidden {
		t.Errorf("status %d after the removal, want 403", got)
	}
}

// Promoting from the panel flags the account and audits who did it to whom.
func TestGrantAdminHandlerFlagsTheAccountAndAudits(t *testing.T) {
	a, store, token, _ := newTestAdmin(t, "acct-grant1")
	other, err := store.GetOrCreateUser(context.Background(), "sub-other", "other@uni-osnabrueck.de", "Other")
	if err != nil {
		t.Fatal(err)
	}

	rec := postAccount(a.GrantAdmin, token, "/admin/admins", other.ID)

	if rec.Code != http.StatusFound {
		t.Errorf("status %d, want a redirect", rec.Code)
	}
	if !isAdmin(t, store, other.ID) {
		t.Error("the account was not flagged")
	}
	e := lastAudit(t, store)
	if e == nil || e.Action != database.AuditAdminGranted {
		t.Fatalf("audit = %+v, want an %s event", e, database.AuditAdminGranted)
	}
	if e.ActorEmail != "acting@uni-osnabrueck.de" || e.SubjectEmail != "other@uni-osnabrueck.de" ||
		e.SubjectID == nil || *e.SubjectID != other.ID {
		t.Errorf("audit = %+v, want the acting admin as actor and the account as subject", e)
	}
}

// An id that names no account, from a stale or hand-made form, promotes nobody
// and audits nothing.
func TestGrantAdminHandlerRefusesAnUnknownAccount(t *testing.T) {
	a, store, token, _ := newTestAdmin(t, "acct-grant2")

	rec := postAccount(a.GrantAdmin, token, "/admin/admins", 404)

	if rec.Code != http.StatusFound {
		t.Errorf("status %d, want a redirect back to the panel", rec.Code)
	}
	if n, _ := store.CountAdmins(context.Background()); n != 0 {
		t.Errorf("%d admins after promoting a missing account, want 0", n)
	}
	if e := lastAudit(t, store); e != nil {
		t.Errorf("audit = %+v, want nothing recorded", e)
	}
}

// The foot-gun: an admin removing their own rights could leave the panel with
// nobody in charge, so the request is refused.
func TestRevokeAdminHandlerRefusesSelfRevoke(t *testing.T) {
	a, store, token, actor := newTestAdmin(t, "acct-revoke1")
	if err := store.SetUserAdmin(context.Background(), actor.ID, true); err != nil {
		t.Fatal(err)
	}

	postAccount(a.RevokeAdmin, token, "/admin/admins/revoke", actor.ID)

	if !isAdmin(t, store, actor.ID) {
		t.Error("an admin removed their own rights")
	}
	if e := lastAudit(t, store); e != nil {
		t.Errorf("audit = %+v, want nothing recorded for a refused removal", e)
	}
}

// Removing someone else clears the flag and is audited.
func TestRevokeAdminHandlerUnflagsAndAudits(t *testing.T) {
	a, store, token, _ := newTestAdmin(t, "acct-revoke2")
	ctx := context.Background()
	other, err := store.GetOrCreateUser(ctx, "sub-other", "other@uni-osnabrueck.de", "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserAdmin(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}

	postAccount(a.RevokeAdmin, token, "/admin/admins/revoke", other.ID)

	if isAdmin(t, store, other.ID) {
		t.Error("the flag survived the removal")
	}
	e := lastAudit(t, store)
	if e == nil || e.Action != database.AuditAdminRevoked || e.SubjectEmail != "other@uni-osnabrueck.de" {
		t.Errorf("audit = %+v, want an %s event for the account", e, database.AuditAdminRevoked)
	}
}

// The acting admin's own row is marked and offers no remove button; every
// other admin's row does.
func TestAdminRowsMarkSelfAsUnremovable(t *testing.T) {
	admins := []database.User{
		{ID: 1, Email: "acting@uni-osnabrueck.de"},
		{ID: 2, Email: "other@uni-osnabrueck.de"},
	}

	rows := adminRows(admins, 1)

	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two", rows)
	}
	if !rows[0].IsSelf || rows[0].Removable {
		t.Errorf("own row = %+v, want it marked self and not removable", rows[0])
	}
	if rows[1].IsSelf || !rows[1].Removable {
		t.Errorf("other row = %+v, want it removable and not self", rows[1])
	}
}

// The promotion form offers only accounts that are not admins yet.
func TestAdminCandidatesLeaveOutAdmins(t *testing.T) {
	users := []database.User{
		{ID: 1, Email: "admin@uni-osnabrueck.de", IsAdmin: true},
		{ID: 2, Email: "student@uni-osnabrueck.de"},
	}

	got := adminCandidates(users)

	if len(got) != 1 || got[0].ID != 2 {
		t.Errorf("candidates = %+v, want only the account without the flag", got)
	}
}

// When the login system decides, the next login would overwrite whatever the
// panel set, so granting and removing are refused and nothing is audited.
func TestAdminChangesRefusedWhenGroupManaged(t *testing.T) {
	a, store, token, _ := newTestAdmin(t, "acct-group1")
	a.cfg.AdminGroup = "ai-admin"
	other, err := store.GetOrCreateUser(context.Background(), "sub-other", "other@uni-osnabrueck.de", "Other")
	if err != nil {
		t.Fatal(err)
	}

	if rec := postAccount(a.GrantAdmin, token, "/admin/admins", other.ID); rec.Code != http.StatusFound {
		t.Errorf("grant: status %d, want a redirect back to the panel", rec.Code)
	}
	if isAdmin(t, store, other.ID) {
		t.Error("the panel granted admin while the login system decides")
	}

	if err := store.SetUserAdmin(context.Background(), other.ID, true); err != nil {
		t.Fatal(err)
	}
	if rec := postAccount(a.RevokeAdmin, token, "/admin/admins/revoke", other.ID); rec.Code != http.StatusFound {
		t.Errorf("revoke: status %d, want a redirect back to the panel", rec.Code)
	}
	if !isAdmin(t, store, other.ID) {
		t.Error("the panel removed admin while the login system decides")
	}
	if e := lastAudit(t, store); e != nil {
		t.Errorf("audit = %+v, want nothing recorded", e)
	}
}
