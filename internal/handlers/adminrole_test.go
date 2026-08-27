package handlers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

// idTokenWithRoles builds a token carrying realm roles, the shape Keycloak
// emits once a mapper adds them to the ID token.
func idTokenWithRoles(t *testing.T, roles ...string) string {
	t.Helper()
	claims := map[string]any{"sub": "sub-1"}
	if roles != nil {
		claims["realm_access"] = map[string]any{"roles": roles}
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "h." + base64.RawURLEncoding.EncodeToString(body) + ".s"
}

// adminGate builds the admin middleware over a store holding one user, and
// returns the status code the gate produces for the given token and config.
func adminGate(t *testing.T, name string, cfg *config.Config, idToken string) int {
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
	user, err := store.GetOrCreateUser(ctx, "sub-1", "nobody@uni-osnabrueck.de", "N")
	if err != nil {
		t.Fatal(err)
	}

	sessions := session.NewManager(store, time.Hour, false)
	token, err := sessions.Create(ctx, user.ID, idToken)
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

// The point of the role: someone holding it reaches the panel without being
// named in any list this repo maintains.
func TestAdminGateAcceptsTheRole(t *testing.T) {
	cfg := &config.Config{AdminRole: "ai-self-service-admin"}
	got := adminGate(t, "ar1", cfg, idTokenWithRoles(t, "offline_access", "ai-self-service-admin"))

	if got != http.StatusOK {
		t.Errorf("status %d for a user holding the role, want 200", got)
	}
}

// Holding some other role is not enough.
func TestAdminGateRejectsOtherRoles(t *testing.T) {
	cfg := &config.Config{AdminRole: "ai-self-service-admin"}
	got := adminGate(t, "ar2", cfg, idTokenWithRoles(t, "offline_access", "uma_authorization"))

	if got != http.StatusForbidden {
		t.Errorf("status %d for a user without the role, want 403", got)
	}
}

// A realm with no mapper emits no roles. Admin must then still work from the
// configured list, or configuring a role would lock everyone out of a realm
// that cannot emit one.
func TestAdminGateFallsBackToTheListWithoutARoleClaim(t *testing.T) {
	cfg := &config.Config{
		AdminRole: "ai-self-service-admin",
		AdminIDs:  []string{"sub-1"},
	}
	got := adminGate(t, "ar3", cfg, idTokenWithRoles(t)) // no realm_access claim

	if got != http.StatusOK {
		t.Errorf("status %d for a listed user on a realm without roles, want 200", got)
	}
}

// With no role configured, a realm that happens to emit roles must not grant
// the panel to whoever holds one.
func TestAdminGateIgnoresRolesWhenUnconfigured(t *testing.T) {
	cfg := &config.Config{} // no AdminRole, no AdminIDs
	got := adminGate(t, "ar4", cfg, idTokenWithRoles(t, "ai-self-service-admin", "admin"))

	if got != http.StatusForbidden {
		t.Errorf("status %d with ADMIN_ROLE unset, want 403", got)
	}
}
