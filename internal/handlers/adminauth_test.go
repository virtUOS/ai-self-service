package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/session"
)

// newAuthTestStore opens a migrated in-memory store for the resolver tests.
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

// The role wins over everything: a deployment managing admins in the IdP must
// not have that decision second-guessed by a row in this database.
func TestResolveAdminPrefersTheRole(t *testing.T) {
	store := newAuthTestStore(t, "resolve1")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, "person@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminRole: "ai-admin", AdminIDs: []string{"sub-1"}}

	src, _ := resolveAdmin(ctx, cfg, store, idTokenWithRoles(t, "ai-admin"), "sub-1", "person@uni-osnabrueck.de")

	if src != adminSourceRole {
		t.Errorf("source = %v, want the role to win", src)
	}
}

// The env list outranks the table, so the panel always reports an env admin as
// unremovable rather than offering a remove button that would do nothing.
func TestResolveAdminPrefersTheEnvListOverAGrant(t *testing.T) {
	store := newAuthTestStore(t, "resolve2")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, "person@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminIDs: []string{"sub-1"}}

	src, bySubject := resolveAdmin(ctx, cfg, store, "", "sub-1", "person@uni-osnabrueck.de")

	if src != adminSourceEnv {
		t.Errorf("source = %v, want the env list", src)
	}
	if !bySubject {
		t.Error("bySubject = false, want true for a subject entry")
	}
}

// With neither role nor env entry, the table decides.
func TestResolveAdminFallsBackToAGrant(t *testing.T) {
	store := newAuthTestStore(t, "resolve3")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, "person@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}

	src, _ := resolveAdmin(ctx, cfg, store, "", "sub-1", "person@uni-osnabrueck.de")

	if src != adminSourceGrant {
		t.Errorf("source = %v, want the grant table", src)
	}
}

// Nobody is an admin by default.
func TestResolveAdminRefusesAStranger(t *testing.T) {
	store := newAuthTestStore(t, "resolve4")
	ctx := context.Background()
	cfg := &config.Config{}

	src, _ := resolveAdmin(ctx, cfg, store, "", "sub-1", "stranger@uni-osnabrueck.de")

	if src.isAdmin() {
		t.Errorf("source = %v, want no admin rights", src)
	}
}

// adminGateWithGrant runs the admin middleware for a user who holds a grant in
// the table and no other source of rights, returning the status code.
func adminGateWithGrant(t *testing.T, name string, cfg *config.Config, email string) int {
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
	user, err := store.GetOrCreateUser(ctx, "sub-1", email, "N")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GrantAdmin(ctx, email, "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	sessions := session.NewManager(store, time.Hour, false)
	token, err := sessions.Create(ctx, user.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	admin := &Admin{cfg: cfg, store: store, sessions: sessions}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	admin.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code
}

// The point of the feature: a grant made in the panel opens the panel, with
// nothing in the deployment naming this person.
func TestAdminGateAcceptsAGrant(t *testing.T) {
	got := adminGateWithGrant(t, "gate1", &config.Config{}, "granted@uni-osnabrueck.de")

	if got != http.StatusOK {
		t.Errorf("status %d for a granted admin, want 200", got)
	}
}

// Someone with no grant and no configuration is still refused.
func TestAdminGateStillRefusesAStranger(t *testing.T) {
	got := adminGate(t, "gate2", &config.Config{}, "")

	if got != http.StatusForbidden {
		t.Errorf("status %d for a stranger, want 403", got)
	}
}
