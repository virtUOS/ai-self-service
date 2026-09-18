# UI-Managed Admin Rights Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin grant and revoke admin rights to other users from `/admin`, instead of editing `ADMIN_IDS` in the deployment script and redeploying.

**Architecture:** A third source of admin rights — an `admin_grants` table — is added behind the two existing deploy-time sources. A single resolver applies the precedence (IdP realm role, then `ADMIN_IDS`, then the table) so the admin middleware and the dashboard's nav link cannot drift apart. The env list always grants and has no remove button in the UI, which makes lockout impossible.

**Tech Stack:** Go, [bun](https://bun.uptrace.dev/) ORM over SQLite, chi router, `html/template`, standard-library `testing`.

**Spec:** `docs/superpowers/specs/2026-09-18-ui-managed-admins-design.md`

## Global Constraints

- **Precedence is fixed:** `ADMIN_ROLE` realm role, then `ADMIN_IDS` env list, then the `admin_grants` table. First match wins.
- **`ADMIN_IDS` and `ADMIN_ROLE` entries can never be removed through the UI.** They are the recovery path.
- **Every user-visible string is translated** into German and English in `internal/i18n/messages.go`. `TestAdminHasNoUntranslatedProse`-style tests scan rendered German pages for English prose; hardcoded English in a template fails the build.
- **Audit actions are constants** in `internal/database/models.go`, never string literals at call sites.
- **Migrations are additive** and register via `init()` + `Migrations.MustRegister(up, down)`, following `internal/database/migrations/20240007_budget_quotas.go`.
- **Run the full suite** with `go test ./...` before every commit.
- Go module path is `github.com/virtuos/ai-self-service`.

---

### Task 1: The `admin_grants` table

**Files:**
- Create: `internal/database/migrations/20240008_admin_grants.go`
- Modify: `internal/database/models.go` (append the model and two audit constants)
- Test: `internal/database/admingrant_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `database.AdminGrant` struct with fields `ID int64`, `OIDCSub *string`, `Email string`, `GrantedByEmail string`, `CreatedAt time.Time`; audit action constants `database.AuditAdminGranted = "admin.granted"` and `database.AuditAdminRevoked = "admin.revoked"`.

- [ ] **Step 1: Write the failing test**

Create `internal/database/admingrant_test.go`. Look at an existing store test such as `internal/database/audit_test.go` for how a test store is opened; reuse that helper rather than opening a database by hand.

```go
package database

import (
	"context"
	"testing"
)

// The table must exist after migrations and hold a grant that has no subject
// yet: an admin can be named before they have ever logged in.
func TestAdminGrantsTableAcceptsAGrantWithoutASubject(t *testing.T) {
	store := newTestStore(t, "admingrants1")
	ctx := context.Background()

	g := &AdminGrant{Email: "new.admin@uni-osnabrueck.de", GrantedByEmail: "boss@uni-osnabrueck.de"}
	if _, err := store.db.NewInsert().Model(g).Exec(ctx); err != nil {
		t.Fatalf("insert grant without subject: %v", err)
	}
	if g.ID == 0 {
		t.Error("insert did not assign an id")
	}

	var got AdminGrant
	if err := store.db.NewSelect().Model(&got).Where("email = ?", g.Email).Scan(ctx); err != nil {
		t.Fatalf("read grant back: %v", err)
	}
	if got.OIDCSub != nil {
		t.Errorf("OIDCSub = %v, want nil for a grant made before first login", *got.OIDCSub)
	}
	if got.GrantedByEmail != "boss@uni-osnabrueck.de" {
		t.Errorf("GrantedByEmail = %q, want the granting admin", got.GrantedByEmail)
	}
}

// Email is the identity of a grant: granting the same address twice must not
// create a second row.
func TestAdminGrantsEmailIsUnique(t *testing.T) {
	store := newTestStore(t, "admingrants2")
	ctx := context.Background()

	first := &AdminGrant{Email: "dup@uni-osnabrueck.de", GrantedByEmail: "boss@uni-osnabrueck.de"}
	if _, err := store.db.NewInsert().Model(first).Exec(ctx); err != nil {
		t.Fatalf("insert first grant: %v", err)
	}
	second := &AdminGrant{Email: "dup@uni-osnabrueck.de", GrantedByEmail: "boss@uni-osnabrueck.de"}
	if _, err := store.db.NewInsert().Model(second).Exec(ctx); err == nil {
		t.Error("inserting a duplicate email succeeded, want a unique-constraint error")
	}
}
```

If `newTestStore` does not exist in the `database` package, copy the setup from the top of `internal/database/audit_test.go` into a helper of that name in this new file, so later tasks can reuse it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/database/ -run TestAdminGrants -v`
Expected: FAIL — `undefined: AdminGrant`.

- [ ] **Step 3: Add the model and the audit constants**

Append to `internal/database/models.go`:

```go
// AdminGrant is admin rights given through the admin panel, as opposed to the
// deploy-time ADMIN_ROLE and ADMIN_IDS settings.
//
// OIDCSub is null until the person first logs in: an admin can be named by
// email before they have ever signed in, and the subject is recorded then.
// The subject is what authorises them once known — an email address is
// assigned by the IdP and can be reassigned to someone else, so a grant
// resolving by email alone grants rights to whoever holds the address today.
type AdminGrant struct {
	bun.BaseModel `bun:"table:admin_grants"`

	ID             int64     `bun:"id,pk,autoincrement"`
	OIDCSub        *string   `bun:"oidc_sub"`
	Email          string    `bun:"email,unique,notnull"`
	GrantedByEmail string    `bun:"granted_by_email,notnull"`
	CreatedAt      time.Time `bun:"created_at,notnull"`
}
```

Add to the existing `AuditAction` constant block in the same file, beside `AuditProfileSet`:

```go
	AuditAdminGranted = "admin.granted"
	AuditAdminRevoked = "admin.revoked"
```

- [ ] **Step 4: Write the migration**

Create `internal/database/migrations/20240008_admin_grants.go`:

```go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Admin rights become grantable from the admin panel.
//
// ADMIN_ROLE and ADMIN_IDS keep working and keep their precedence; this table
// only adds admins on top of them. The env list stays a floor the panel cannot
// remove, so no sequence of clicks can lock everyone out of the panel.
//
// oidc_sub is nullable because an admin can be named by email before they have
// ever logged in; it is filled in on their first login and is what authorises
// them thereafter.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE admin_grants (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				oidc_sub TEXT,
				email TEXT NOT NULL UNIQUE,
				granted_by_email TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL
			)`); err != nil {
			return fmt.Errorf("create admin_grants: %w", err)
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `DROP TABLE admin_grants`); err != nil {
			return fmt.Errorf("drop admin_grants: %w", err)
		}
		return nil
	})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/database/ -run TestAdminGrants -v`
Expected: PASS, both tests.

Then run the whole suite to confirm the new migration breaks nothing:
Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/database/models.go internal/database/migrations/20240008_admin_grants.go internal/database/admingrant_test.go
git commit -m "Add the admin_grants table

Admin rights can only be set at deploy time today. This is the storage for
grants made from the panel; nothing reads it yet.

oidc_sub is nullable so an admin can be named before their first login."
```

---

### Task 2: Store methods for grants

**Files:**
- Modify: `internal/database/db.go` (append an `--- Admin grants ---` section, following the existing `--- Profiles ---` comment style)
- Test: `internal/database/admingrant_test.go` (extend)

**Interfaces:**
- Consumes: `database.AdminGrant` from Task 1.
- Produces:
  - `func (s *Store) GrantAdmin(ctx context.Context, email, grantedBy string) error`
  - `func (s *Store) RevokeAdmin(ctx context.Context, email string) error`
  - `func (s *Store) ListAdminGrants(ctx context.Context) ([]AdminGrant, error)`
  - `func (s *Store) IsAdminGranted(ctx context.Context, sub, email string) (bool, error)`
  - `func (s *Store) LinkAdminGrantSubject(ctx context.Context, email, sub string) error`

- [ ] **Step 1: Write the failing tests**

Append to `internal/database/admingrant_test.go`:

```go
// A grant authorises by email before the subject is known, and by subject
// afterwards. The subject is the durable identifier, so once it is recorded a
// change of address must not revoke the rights.
func TestIsAdminGrantedMatchesEmailThenSubject(t *testing.T) {
	store := newTestStore(t, "admingrants3")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "grantee@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	ok, err := store.IsAdminGranted(ctx, "sub-unknown", "grantee@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a grant made by email did not authorise before first login")
	}

	if err := store.LinkAdminGrantSubject(ctx, "grantee@uni-osnabrueck.de", "sub-42"); err != nil {
		t.Fatal(err)
	}

	ok, err = store.IsAdminGranted(ctx, "sub-42", "someone.else@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a linked subject did not authorise after the address changed")
	}
}

// Email matching folds case: IdPs vary in how they present an address, and an
// admin typing it by hand should not have to match that exactly.
func TestIsAdminGrantedFoldsEmailCase(t *testing.T) {
	store := newTestStore(t, "admingrants4")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "Mixed.Case@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.IsAdminGranted(ctx, "", "mixed.case@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("email match was case-sensitive")
	}
}

// An empty subject must never match a row whose subject is also unset, or a
// user whose IdP omits the claim would take someone else's grant.
func TestIsAdminGrantedIgnoresAnEmptySubject(t *testing.T) {
	store := newTestStore(t, "admingrants5")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "someone@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.IsAdminGranted(ctx, "", "unrelated@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("an empty subject matched a grant with no subject recorded")
	}
}

// Granting an address that is already granted is not an error: the admin
// pressed the button twice, and the outcome they asked for already holds.
func TestGrantAdminIsIdempotent(t *testing.T) {
	store := newTestStore(t, "admingrants6")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "twice@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantAdmin(ctx, "twice@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatalf("second grant of the same address: %v", err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Errorf("got %d grants, want 1", len(grants))
	}
}

// Revoking removes the row, so the resolver stops authorising.
func TestRevokeAdminRemovesTheGrant(t *testing.T) {
	store := newTestStore(t, "admingrants7")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "gone@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAdmin(ctx, "gone@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.IsAdminGranted(ctx, "", "gone@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a revoked grant still authorises")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/database/ -run "TestIsAdminGranted|TestGrantAdmin|TestRevokeAdmin" -v`
Expected: FAIL — `store.GrantAdmin undefined`.

- [ ] **Step 3: Implement the store methods**

Append to `internal/database/db.go`:

```go
// --- Admin grants ---

// GrantAdmin records admin rights for an email address.
//
// Granting an address that already holds a grant is a no-op rather than an
// error: the admin pressed the button twice and the state they asked for
// already holds.
func (s *Store) GrantAdmin(ctx context.Context, email, grantedBy string) error {
	_, err := s.db.NewInsert().
		Model(&AdminGrant{Email: email, GrantedByEmail: grantedBy, CreatedAt: time.Now()}).
		On("CONFLICT (email) DO NOTHING").
		Exec(ctx)
	return err
}

// RevokeAdmin removes a grant. Removing one that does not exist is not an
// error, for the same reason granting twice is not.
func (s *Store) RevokeAdmin(ctx context.Context, email string) error {
	_, err := s.db.NewDelete().Model((*AdminGrant)(nil)).
		Where("email = ? COLLATE NOCASE", email).Exec(ctx)
	return err
}

// ListAdminGrants returns every grant made through the panel, oldest first.
func (s *Store) ListAdminGrants(ctx context.Context) ([]AdminGrant, error) {
	var grants []AdminGrant
	err := s.db.NewSelect().Model(&grants).OrderExpr("created_at ASC, id ASC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return grants, nil
}

// IsAdminGranted reports whether a grant covers this subject or address.
//
// The subject is checked first and an empty one never matches, so a user whose
// IdP omits the claim cannot take a grant whose subject is not yet recorded.
func (s *Store) IsAdminGranted(ctx context.Context, sub, email string) (bool, error) {
	q := s.db.NewSelect().Model((*AdminGrant)(nil))
	if sub != "" {
		q = q.Where("oidc_sub = ? OR email = ? COLLATE NOCASE", sub, email)
	} else {
		q = q.Where("email = ? COLLATE NOCASE", email)
	}
	count, err := q.Count(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// LinkAdminGrantSubject records the OIDC subject against a grant made by email.
//
// Called on login: from then on the grant is anchored to the person rather than
// to whoever holds the address.
func (s *Store) LinkAdminGrantSubject(ctx context.Context, email, sub string) error {
	_, err := s.db.NewUpdate().Model((*AdminGrant)(nil)).
		Set("oidc_sub = ?", sub).
		Where("email = ? COLLATE NOCASE AND oidc_sub IS NULL", email).
		Exec(ctx)
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/database/ -v -run "TestAdminGrants|TestIsAdminGranted|TestGrantAdmin|TestRevokeAdmin"`
Expected: PASS.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/db.go internal/database/admingrant_test.go
git commit -m "Add store methods for admin grants

Matching prefers the OIDC subject and falls back to a case-folded email,
mirroring config.IsAdmin. An empty subject never matches, so a user whose
IdP omits the claim cannot take a grant whose subject is unrecorded."
```

---

### Task 3: The precedence resolver

**Files:**
- Create: `internal/handlers/adminauth.go`
- Test: `internal/handlers/adminauth_test.go`

**Interfaces:**
- Consumes: `store.IsAdminGranted` from Task 2; `cfg.HasAdminRole` and `cfg.IsAdmin` from `internal/config/config.go`; `oidcpkg.RealmRoles` from `internal/oidc`.
- Produces:
  - `type adminSource int` with constants `adminSourceNone`, `adminSourceRole`, `adminSourceEnv`, `adminSourceGrant`.
  - `func resolveAdmin(ctx context.Context, cfg *config.Config, store *database.Store, idToken, sub, email string) (adminSource, bool)` — the bool is `bySubject`, meaningful only for `adminSourceEnv` and `adminSourceGrant`.
  - `func (s adminSource) isAdmin() bool`

This is the single place the precedence lives. Task 4 and Task 5 both call it rather than re-deriving it.

- [ ] **Step 1: Write the failing tests**

Create `internal/handlers/adminauth_test.go`:

```go
package handlers

import (
	"context"
	"testing"

	"github.com/virtuos/ai-self-service/internal/config"
)

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
```

Add a store helper to the same file. `newTestStore` from Task 1 lives in the `database` package and is not visible here:

```go
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
```

with the imports `database/sql`, `github.com/uptrace/bun`, `github.com/uptrace/bun/dialect/sqlitedialect`, `github.com/uptrace/bun/driver/sqliteshim` and `github.com/virtuos/ai-self-service/internal/database`. `idTokenWithRoles` already exists in `internal/handlers/adminrole_test.go`, same package — do not redefine it.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/handlers/ -run TestResolveAdmin -v`
Expected: FAIL — `undefined: resolveAdmin`.

- [ ] **Step 3: Implement the resolver**

Create `internal/handlers/adminauth.go`:

```go
package handlers

import (
	"context"
	"log/slog"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	oidcpkg "github.com/virtuos/ai-self-service/internal/oidc"
)

// adminSource is where a user's admin rights come from. The zero value is no
// rights, so a resolver that fails closed denies.
type adminSource int

const (
	adminSourceNone adminSource = iota
	adminSourceRole
	adminSourceEnv
	adminSourceGrant
)

func (s adminSource) isAdmin() bool { return s != adminSourceNone }

// resolveAdmin decides whether a user holds admin rights and on what basis.
//
// This is the only place the precedence lives. The admin middleware and the
// dashboard's nav link both call it: deriving the answer separately is how the
// panel and the link that leads to it drift apart.
//
// Order is role, then the configured list, then the grants table. The role
// wins so that a deployment managing admins in the IdP is not second-guessed
// by a row here, and the list outranks the table so an entry an operator put
// in the deployment cannot be removed by a click.
//
// The second return is whether an env or table match was made on the subject
// rather than the address; callers use it to warn about the weaker form.
func resolveAdmin(ctx context.Context, cfg *config.Config, store *database.Store,
	idToken, sub, email string) (adminSource, bool) {

	if cfg.HasAdminRole(oidcpkg.RealmRoles(idToken)) {
		return adminSourceRole, true
	}
	if admin, bySubject := cfg.IsAdmin(sub, email); admin {
		return adminSourceEnv, bySubject
	}
	granted, err := store.IsAdminGranted(ctx, sub, email)
	if err != nil {
		// Fail closed: a database blip must not hand out the panel.
		slog.Error("check admin grants", "email", email, "err", err)
		return adminSourceNone, false
	}
	if granted {
		return adminSourceGrant, sub != ""
	}
	return adminSourceNone, false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/handlers/ -run TestResolveAdmin -v`
Expected: PASS, all four.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/adminauth.go internal/handlers/adminauth_test.go
git commit -m "Add the admin precedence resolver

One place decides admin rights: role, then the configured list, then the
grants table. The middleware and the dashboard nav link will both call it
rather than deriving the answer twice.

A database error fails closed."
```

---

### Task 4: Route the middleware and dashboard through the resolver

**Files:**
- Modify: `internal/handlers/admin.go:137-178` (`Admin.Middleware`)
- Modify: `internal/handlers/ui.go:143-145` (the `isAdmin` flag)
- Test: `internal/handlers/adminauth_test.go` (extend)

**Interfaces:**
- Consumes: `resolveAdmin` and `adminSource` from Task 3.
- Produces: no new exported names. After this task a grant in the table opens the panel.

- [ ] **Step 1: Write the failing test**

Append to `internal/handlers/adminauth_test.go`. `adminGate` in `internal/handlers/adminrole_test.go` builds the middleware over a store already, but creates its own store internally; add a variant that lets the test seed a grant first. Put it in this file:

```go
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
```

Add `net/http`, `net/http/httptest`, `time` and `github.com/virtuos/ai-self-service/internal/session` to this file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/handlers/ -run "TestAdminGateAcceptsAGrant" -v`
Expected: FAIL with status 403 — the middleware does not consult the table yet.

- [ ] **Step 3: Rewrite the middleware's decision**

In `internal/handlers/admin.go`, replace the body of `Middleware` after the session lookup (the block currently running from the `HasAdminRole` check to the final `next.ServeHTTP`) with:

```go
		src, bySubject := resolveAdmin(r.Context(), a.cfg, a.store,
			su.IDToken, su.User.OIDCSub, su.User.Email)
		if !src.isAdmin() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if (src == adminSourceEnv || src == adminSourceGrant) && !bySubject {
			// The entry that granted this is an email address, which the IdP
			// can reassign. Say so once per request rather than silently
			// relying on it, so an operator can migrate the allowlist.
			slog.Warn("admin granted by email rather than OIDC subject",
				"email", su.User.Email, "sub", su.User.OIDCSub, "source", src)
		}
		next.ServeHTTP(w, r)
```

Drop the now-unused `oidcpkg` import from `admin.go` if nothing else in the file uses it — `go build ./...` will tell you.

In `internal/handlers/ui.go`, replace the two-line `isAdmin` derivation at lines 143-145 with:

```go
	src, _ := resolveAdmin(r.Context(), u.cfg, u.store, su.IDToken, su.User.OIDCSub, su.User.Email)
	isAdmin := src.isAdmin()
```

and drop `oidcpkg` from `ui.go`'s imports if it becomes unused.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/handlers/ -v`
Expected: PASS. The existing `TestAdminGate*` tests in `adminrole_test.go` must still pass — they cover the role and env paths and are the regression net for the precedence.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/admin.go internal/handlers/ui.go internal/handlers/adminauth_test.go
git commit -m "Consult admin grants in the middleware and the nav link

Both now call the resolver instead of deriving admin rights themselves, so
a grant opens the panel and shows the nav link together."
```

---

### Task 5: Record the subject on login

**Files:**
- Modify: `internal/database/db.go` (`GetOrCreateUser`, around line 251)
- Test: `internal/database/admingrant_test.go` (extend)

**Interfaces:**
- Consumes: `LinkAdminGrantSubject` from Task 2.
- Produces: no new names. A grant made by email is anchored to a subject the first time that person logs in.

- [ ] **Step 1: Write the failing test**

Append to `internal/database/admingrant_test.go`:

```go
// A grant made before the person ever logged in is anchored to their subject
// the moment they do, so it stops depending on the address.
func TestLoginLinksAPendingGrantToTheSubject(t *testing.T) {
	store := newTestStore(t, "admingrants8")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "future@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateUser(ctx, "sub-99", "future@uni-osnabrueck.de", "Future Admin"); err != nil {
		t.Fatal(err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("got %d grants, want 1", len(grants))
	}
	if grants[0].OIDCSub == nil || *grants[0].OIDCSub != "sub-99" {
		t.Errorf("OIDCSub = %v, want sub-99 recorded on first login", grants[0].OIDCSub)
	}
}

// Logging in must not attach a subject to somebody else's grant.
func TestLoginLeavesOtherGrantsAlone(t *testing.T) {
	store := newTestStore(t, "admingrants9")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "other@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateUser(ctx, "sub-100", "unrelated@uni-osnabrueck.de", "Someone"); err != nil {
		t.Fatal(err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if grants[0].OIDCSub != nil {
		t.Errorf("OIDCSub = %v, want an unrelated login to leave it unset", *grants[0].OIDCSub)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/database/ -run TestLogin -v`
Expected: FAIL — `OIDCSub = <nil>, want sub-99`.

- [ ] **Step 3: Link the subject in `GetOrCreateUser`**

In `internal/database/db.go`, in `GetOrCreateUser`, call the linker on both paths — the existing-user branch and after the insert. Add before `return user, nil` in each branch:

```go
	// Anchor any grant made by address alone to this subject, now that it is
	// known. Failing here must not block the login: the grant still resolves
	// by address until the next attempt.
	if err := s.LinkAdminGrantSubject(ctx, email, sub); err != nil {
		slog.Error("link admin grant subject", "email", email, "err", err)
	}
```

Add `log/slog` to the file's imports if it is not already there.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/database/ -run TestLogin -v`
Expected: PASS, both.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/db.go internal/database/admingrant_test.go
git commit -m "Record the OIDC subject on a grant at first login

A grant made by address is anchored to the person the moment they sign in,
so it no longer depends on an address the IdP can reassign. A failure is
logged, not fatal: the grant still resolves by address until next time."
```

---

### Task 6: Grant and revoke handlers

**Files:**
- Modify: `internal/handlers/admin.go` (add two handlers beside `SetUserProfile`, around line 359)
- Modify: `cmd/server/main.go:151-159` (two routes)
- Test: `internal/handlers/admingrant_test.go` (create)

**Interfaces:**
- Consumes: `GrantAdmin`, `RevokeAdmin` from Task 2; `resolveAdmin` from Task 3; `a.audit` and `a.actorEmail` already in `admin.go`.
- Produces:
  - `func (a *Admin) GrantAdmin(w http.ResponseWriter, r *http.Request)` on `POST /admin/admins`, reading form field `email`.
  - `func (a *Admin) RevokeAdmin(w http.ResponseWriter, r *http.Request)` on `POST /admin/admins/revoke`, reading form field `email`.

- [ ] **Step 1: Write the failing tests**

Create `internal/handlers/admingrant_test.go`. Follow the request-construction style of the existing handler tests; post form values with `strings.NewReader(form.Encode())` and `Content-Type: application/x-www-form-urlencoded`.

```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
)

// Granting from the panel writes a grant and audits who did it.
func TestGrantAdminHandlerRecordsAndAudits(t *testing.T) {
	a, store := newGrantTestAdmin(t, "grant1")
	ctx := context.Background()

	form := url.Values{"email": {"new@uni-osnabrueck.de"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	a, store := newGrantTestAdmin(t, "grant2")
	ctx := context.Background()

	form := url.Values{"email": {"not-an-address"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	a, store := newGrantTestAdmin(t, "grant3")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, actingAdminEmail, "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"email": {actingAdminEmail}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	a, store := newGrantTestAdmin(t, "grant4")
	ctx := context.Background()
	if err := store.GrantAdmin(ctx, "victim@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"email": {"victim@uni-osnabrueck.de"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/admins/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
```

Add the helper and constant to the same file. It must build an `Admin` whose session resolves to a known acting admin, because `a.actorEmail` reads the session:

```go
// actingAdminEmail is who the test's session belongs to — the admin pressing
// the buttons.
const actingAdminEmail = "acting@uni-osnabrueck.de"

// newGrantTestAdmin builds an Admin whose session belongs to actingAdminEmail.
func newGrantTestAdmin(t *testing.T, name string) (*Admin, *database.Store) {
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
```

The helper returns three values: `func newGrantTestAdmin(t *testing.T, name string) (*Admin, *database.Store, string)`. The handlers read the acting admin from the session cookie, so every test in this file attaches it to its request:

```go
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
```

Adjust the four tests above to destructure three values — `a, store, token := newGrantTestAdmin(t, "grant1")` — and add that line after each `httptest.NewRequest`. Without the cookie, `a.actorEmail` returns `"unknown"` and the self-revoke test passes for the wrong reason.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/handlers/ -run "TestGrantAdminHandler|TestRevokeAdminHandler" -v`
Expected: FAIL — `a.GrantAdmin undefined`.

- [ ] **Step 3: Implement the handlers**

Add to `internal/handlers/admin.go`, beside `SetUserProfile`:

```go
// GrantAdmin handles POST /admin/admins, giving an address the admin panel.
func (a *Admin) GrantAdmin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if _, err := mail.ParseAddress(email); err != nil {
		http.Redirect(w, r, "/admin?flash=Enter+a+valid+email+address#admins", http.StatusFound)
		return
	}

	if err := a.store.GrantAdmin(r.Context(), email, a.actorEmail(r)); err != nil {
		slog.Error("grant admin", "email", email, "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+grant+admin#admins", http.StatusFound)
		return
	}
	a.audit(r, database.AuditAdminGranted, email, nil, "admin granted")
	http.Redirect(w, r, "/admin?flash=Admin+granted#admins", http.StatusFound)
}

// RevokeAdmin handles POST /admin/admins/revoke.
//
// An admin cannot remove their own rights: doing so would need the deployment
// edited to get them back, which is the very thing this feature exists to
// avoid.
func (a *Admin) RevokeAdmin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if strings.EqualFold(email, a.actorEmail(r)) {
		http.Redirect(w, r, "/admin?flash=You+cannot+remove+your+own+admin+rights#admins", http.StatusFound)
		return
	}

	// An address in ADMIN_IDS keeps its rights whatever this table says, so
	// deleting a row for it would report a revoke that did not happen. Say so
	// instead, and do not audit it.
	if admin, _ := a.cfg.IsAdmin("", email); admin {
		http.Redirect(w, r, "/admin?flash=That+admin+comes+from+the+configuration+and+cannot+be+removed+here#admins", http.StatusFound)
		return
	}

	if err := a.store.RevokeAdmin(r.Context(), email); err != nil {
		slog.Error("revoke admin", "email", email, "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+revoke+admin#admins", http.StatusFound)
		return
	}
	a.audit(r, database.AuditAdminRevoked, email, nil, "admin revoked")
	http.Redirect(w, r, "/admin?flash=Admin+revoked#admins", http.StatusFound)
}
```

Add `net/mail` to the imports of `admin.go`.

Register the routes in `cmd/server/main.go`, inside the existing `r.Route("/admin", ...)` block:

```go
		r.Post("/admins", admin.GrantAdmin)
		r.Post("/admins/revoke", admin.RevokeAdmin)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/handlers/ -run "TestGrantAdminHandler|TestRevokeAdminHandler" -v`
Expected: PASS, all four.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/admin.go internal/handlers/admingrant_test.go cmd/server/main.go
git commit -m "Add grant and revoke admin handlers

Both audit. Self-revoke is refused: undoing it would need the deployment
edited, which is what this feature exists to avoid. A malformed address is
rejected rather than stored."
```

---

### Task 7: The Admins tab

**Files:**
- Modify: `web/templates/admin.html` (tab button at line 35, new panel after the users panel ending near line 245, hash routing near line 388)
- Modify: `internal/handlers/admin.go` (`adminData` struct near line 173, and `Panel`)
- Modify: `internal/i18n/messages.go` (new keys)
- Test: `internal/handlers/admingrant_test.go` (extend)

**Interfaces:**
- Consumes: `ListAdminGrants` from Task 2; `resolveAdmin`/`adminSource` from Task 3; the handlers from Task 6.
- Produces: `adminRow` view struct with fields `Email string`, `Source string` (one of `"role"`, `"config"`, `"granted"`), `Removable bool`, `IsSelf bool`; `adminData.Admins []adminRow`.

- [ ] **Step 1: Write the failing test**

Append to `internal/handlers/admingrant_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/handlers/ -run TestAdminRows -v`
Expected: FAIL — `undefined: adminRows`.

- [ ] **Step 3: Build the view rows**

Add to `internal/handlers/adminauth.go`:

```go
// adminRow is one line of the Admins tab.
type adminRow struct {
	Email     string
	Source    string // "role", "config" or "granted"
	Removable bool
	IsSelf    bool
}

// adminRows lists every admin the panel can show, marking which ones it may
// remove.
//
// Entries from ADMIN_IDS are shown but never removable: the list outranks the
// table, so a remove button on one would silently do nothing. The acting
// admin's own row is not removable either — see Admin.RevokeAdmin.
//
// ADMIN_ROLE cannot be enumerated: role membership lives in the IdP and this
// process only ever sees the token of whoever is currently signed in. The
// template says so rather than implying the list is complete.
func adminRows(cfg *config.Config, grants []database.AdminGrant, actor string) []adminRow {
	rows := make([]adminRow, 0, len(cfg.AdminIDs)+len(grants))
	for _, id := range cfg.AdminIDs {
		rows = append(rows, adminRow{
			Email:  id,
			Source: "config",
			IsSelf: strings.EqualFold(id, actor),
		})
	}
	for _, g := range grants {
		rows = append(rows, adminRow{
			Email:     g.Email,
			Source:    "granted",
			Removable: !strings.EqualFold(g.Email, actor),
			IsSelf:    strings.EqualFold(g.Email, actor),
		})
	}
	return rows
}
```

Add `strings` to that file's imports.

In `internal/handlers/admin.go`, add to `adminData`:

```go
	// Admins are the rows of the Admins tab. AdminRoleName is non-empty when
	// ADMIN_ROLE is configured, so the tab can say that role holders are
	// admins too without being able to list them.
	Admins        []adminRow
	AdminRoleName string
```

and populate both in `Panel`, alongside the existing loads:

```go
	grants, err := a.store.ListAdminGrants(r.Context())
	if err != nil {
		slog.Error("list admin grants", "err", err)
	}
	data.Admins = adminRows(a.cfg, grants, a.actorEmail(r))
	data.AdminRoleName = a.cfg.AdminRole
```

- [ ] **Step 4: Add the translations**

In `internal/i18n/messages.go`, in the admin group:

```go
	"admin.admins":            {DE: "Administration", EN: "Admins"},
	"admin.admins.note":       {DE: "Wer auf diese Seite zugreifen darf. Einträge aus der Konfiguration können hier nicht entfernt werden.", EN: "Who may reach this page. Entries from the configuration cannot be removed here."},
	"admin.admins.role":       {DE: "Zusätzlich ist jede Person Administrator, die im IdP die Rolle %s trägt. Diese Liste kann sie nicht anzeigen.", EN: "Anyone holding the %s role in the IdP is an admin as well. This list cannot show them."},
	"admin.admins.source":     {DE: "Quelle", EN: "Source"},
	"admin.admins.fromconfig": {DE: "aus der Konfiguration", EN: "from configuration"},
	"admin.admins.granted":    {DE: "hier vergeben", EN: "granted here"},
	"admin.admins.self":       {DE: "Sie", EN: "you"},
	"admin.admins.add":        {DE: "E-Mail-Adresse", EN: "Email address"},
	"admin.admins.grant":      {DE: "Zum Administrator machen", EN: "Make admin"},
	"admin.admins.revoke":     {DE: "Entfernen", EN: "Remove"},
	"admin.admins.none":       {DE: "Keine hier vergebenen Administratorrechte.", EN: "No admin rights granted here."},
```

- [ ] **Step 5: Add the tab**

In `web/templates/admin.html`, add the tab button after the users one at line 35:

```html
      <button class="tab-btn" onclick="showTab('admins', this)">{{T .Lang "admin.admins"}}</button>
```

Add the panel after the users panel's closing `</div>`, before the audit panel:

```html
    <!-- ── Admins tab ─────────────────────────────────────── -->
    <div id="tab-admins" class="tab-panel">
      <h2 style="margin-bottom:1.25rem">{{T .Lang "admin.admins"}}</h2>
      <p class="text-muted" style="margin-bottom:1rem;font-size:.9rem">
        {{T .Lang "admin.admins.note"}}
      </p>
      {{if .AdminRoleName}}
      <p class="text-muted" style="margin-bottom:1rem;font-size:.9rem">
        {{printf (T .Lang "admin.admins.role") .AdminRoleName}}
      </p>
      {{end}}

      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{{T .Lang "admin.col.email"}}</th>
              <th>{{T .Lang "admin.admins.source"}}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {{range .Admins}}
            <tr>
              <td>{{.Email}}{{if .IsSelf}} <span class="badge">{{T $.Lang "admin.admins.self"}}</span>{{end}}</td>
              <td>
                {{if eq .Source "config"}}<em class="text-faint">{{T $.Lang "admin.admins.fromconfig"}}</em>
                {{else}}{{T $.Lang "admin.admins.granted"}}{{end}}
              </td>
              <td>
                {{if .Removable}}
                <form method="POST" action="/admin/admins/revoke" style="display:inline">
                  <input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
                  <input type="hidden" name="email" value="{{.Email}}">
                  <button class="btn btn-sm btn-danger">{{T $.Lang "admin.admins.revoke"}}</button>
                </form>
                {{end}}
              </td>
            </tr>
            {{else}}
            <tr><td colspan="3" class="text-faint" style="text-align:center">{{T .Lang "admin.admins.none"}}</td></tr>
            {{end}}
          </tbody>
        </table>
      </div>

      <form method="POST" action="/admin/admins"
            style="display:flex;gap:.5rem;align-items:center;margin-top:1rem">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="email" name="email" required
               placeholder="{{T .Lang "admin.admins.add"}}" style="width:auto">
        <button type="submit" class="btn btn-sm btn-primary">{{T .Lang "admin.admins.grant"}}</button>
      </form>
    </div>
```

Add hash routing beside the existing lines near line 388. The audit tab is now the fourth button, so its index changes:

```javascript
if (location.hash === '#admins') showTab('admins', document.querySelectorAll('.tab-btn')[2]);
if (location.hash === '#audit') showTab('audit', document.querySelectorAll('.tab-btn')[3]);
```

Check the existing `#audit` line and update its index rather than adding a second one.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/handlers/ -v`
Expected: PASS, including any template-rendering and untranslated-prose tests.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/templates/admin.html internal/handlers/admin.go internal/handlers/adminauth.go internal/handlers/admingrant_test.go internal/i18n/messages.go
git commit -m "Add the Admins tab

Lists who may reach the panel and where the right comes from. Config
entries and the acting admin's own row have no remove button, since
neither removal would work.

ADMIN_ROLE holders cannot be enumerated — role membership lives in the
IdP — so the tab says so rather than implying the list is complete."
```

---

### Task 8: Document it

**Files:**
- Modify: `README.md` (the `ADMIN_IDS` and `ADMIN_ROLE` rows of the configuration table, and the Features list)

**Interfaces:**
- Consumes: the finished feature.
- Produces: no code.

- [ ] **Step 1: Update the configuration table**

In `README.md`, replace the `ADMIN_ROLE` and `ADMIN_IDS` descriptions:

```markdown
| `ADMIN_ROLE`         | no       | —           | IdP role that grants the admin panel; supersedes `ADMIN_IDS` and grants made in the panel |
| `ADMIN_IDS`          | no       | —           | Comma-separated admins, each an OIDC subject **or** an email. Always grant, and cannot be removed from the panel — this is the recovery path if the last admin is removed |
```

- [ ] **Step 2: Add the feature line**

In the Features list, after the admin panel bullet:

```markdown
- **Admin rights from the panel** — admins can grant and withdraw the admin
  panel for other users without a redeploy; `ADMIN_IDS` still always grants and
  cannot be removed there, so a lockout is always recoverable
```

- [ ] **Step 3: Verify the whole suite one last time**

Run: `go test ./...`
Expected: PASS.

Run: `go build ./...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "Document admin grants

ADMIN_IDS keeps its meaning and gains one: it is the documented recovery
path if the last panel-granted admin is removed."
```

---

## Notes for the executor

- **The precedence is the feature.** If a test seems to want the table to beat the env list, the test is wrong. Re-read the spec's decision 3.
- **Do not add a remove button for config entries** even though it would be easy. It would do nothing, because `resolveAdmin` checks the env list first.
- **`newTestStore` vs `newAuthTestStore`:** the first is in the `database` package, the second in `handlers`. They are separate because Go test helpers do not cross package boundaries; that duplication is intended.
