# Time-Limited Profile Assignments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin assign any profile to a user until a date, after which the assignment reverts on its own — to the default profile, to a profile the admin picked, or by deleting the key if they chose that.

**Architecture:** Three nullable columns on `users` carry the deadline, the destination and the revoke flag. A background job runs every 15 minutes, moves users whose deadline has passed, and pushes the resulting limits to the gateway itself rather than waiting for the user to load the dashboard. Nothing in the schema or the UI names a particular use for the feature.

**Tech Stack:** Go, [bun](https://bun.uptrace.dev/) ORM over SQLite, chi router, `html/template`, standard-library `testing`.

**Spec:** `docs/superpowers/specs/2026-09-18-temporary-profile-assignments-design.md`

## Global Constraints

- **The time limit belongs to the assignment, not the profile.** No new profile kind, no flag on `profiles`, and no column, field, label or identifier naming a particular use (no `research`, no `is_temporary`).
- **Everything is additive and nullable.** A null `profile_expires_at` means a permanent assignment, which is every existing row. No current behaviour changes.
- **A null destination means the default profile**, never "no profile" and never "delete the key".
- **Every user-visible string is translated** into German and English in `internal/i18n/messages.go`. Tests scan rendered German pages for English prose; hardcoded English in a template fails the build.
- **Audit actions are constants** in `internal/database/models.go`, never string literals at call sites.
- **Commit messages must never contain a `Co-Authored-By` line.**
- **Run the full suite** with `go test ./...` before every commit.
- Go module path is `github.com/virtuos/ai-self-service`.

---

### Task 1: Schema for the deadline

**Files:**
- Create: `internal/database/migrations/20240009_profile_expiry.go`
- Modify: `internal/database/models.go` (three fields on `User`, one audit constant)
- Test: `internal/database/profileexpiry_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `User.ProfileExpiresAt *time.Time`, `User.ProfileAfterExpiry *int64`, `User.RevokeKeyAtExpiry bool`; audit constant `database.AuditProfileExpired = "user.profile_expired"`.

- [ ] **Step 1: Write the failing test**

Create `internal/database/profileexpiry_test.go`. Use the existing `migratedStore(t, name)` helper from `internal/database/profile_test.go` — do not write a new store helper.

```go
package database

import (
	"context"
	"testing"
	"time"
)

// Every user that exists today has a permanent assignment. The new columns
// must default to that, or an upgrade would start expiring people.
func TestExistingUsersHaveNoDeadline(t *testing.T) {
	s := migratedStore(t, "pexp1")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-a", "a@uni-osnabrueck.de", "A")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt != nil {
		t.Errorf("ProfileExpiresAt = %v, want nil for a new user", got.ProfileExpiresAt)
	}
	if got.ProfileAfterExpiry != nil {
		t.Errorf("ProfileAfterExpiry = %v, want nil", got.ProfileAfterExpiry)
	}
	if got.RevokeKeyAtExpiry {
		t.Error("RevokeKeyAtExpiry = true, want false")
	}
}

// The three columns must round-trip, including the nullable destination.
func TestDeadlineColumnsRoundTrip(t *testing.T) {
	s := migratedStore(t, "pexp2")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-b", "b@uni-osnabrueck.de", "B")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	dest := int64(7)
	if _, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_expires_at = ?", deadline).
		Set("profile_after_expiry = ?", dest).
		Set("revoke_key_at_expiry = ?", true).
		Where("id = ?", u.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt == nil || !got.ProfileExpiresAt.UTC().Truncate(time.Second).Equal(deadline) {
		t.Errorf("ProfileExpiresAt = %v, want %v", got.ProfileExpiresAt, deadline)
	}
	if got.ProfileAfterExpiry == nil || *got.ProfileAfterExpiry != dest {
		t.Errorf("ProfileAfterExpiry = %v, want %d", got.ProfileAfterExpiry, dest)
	}
	if !got.RevokeKeyAtExpiry {
		t.Error("RevokeKeyAtExpiry = false, want true")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/database/ -run "TestExistingUsersHaveNoDeadline|TestDeadlineColumnsRoundTrip" -v`
Expected: FAIL — `got.ProfileExpiresAt undefined`.

- [ ] **Step 3: Add the fields and the audit constant**

In `internal/database/models.go`, add to the `User` struct after `Profile`:

```go
	// ProfileExpiresAt ends the current assignment. Null means permanent,
	// which is every assignment made before this existed.
	ProfileExpiresAt *time.Time `bun:"profile_expires_at"`

	// ProfileAfterExpiry is where the user lands when the deadline passes.
	// Null means the default profile — never "no access", and never the key
	// being deleted, which is RevokeKeyAtExpiry's job alone.
	ProfileAfterExpiry *int64 `bun:"profile_after_expiry"`

	// RevokeKeyAtExpiry deletes the key instead of switching profiles. Chosen
	// explicitly by the admin; it is never implied by an unset destination.
	RevokeKeyAtExpiry bool `bun:"revoke_key_at_expiry,notnull"`
```

Add to the `AuditAction` constant block:

```go
	AuditProfileExpired = "user.profile_expired" // by the deadline, not an admin
```

- [ ] **Step 4: Write the migration**

Create `internal/database/migrations/20240009_profile_expiry.go`:

```go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// A profile assignment can carry a deadline.
//
// All three columns are nullable or defaulted, so every assignment that exists
// keeps behaving as a permanent one. profile_after_expiry is deliberately
// nullable rather than defaulted to a particular profile: null means "the
// default profile", which is resolved when the deadline fires, so the row does
// not go stale if the default changes in between.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE users ADD COLUMN profile_expires_at TIMESTAMP`,
			`ALTER TABLE users ADD COLUMN profile_after_expiry INTEGER`,
			`ALTER TABLE users ADD COLUMN revoke_key_at_expiry BOOLEAN NOT NULL DEFAULT 0`,
			// The job scans for passed deadlines every 15 minutes; without this
			// that is a full table scan each time.
			`CREATE INDEX idx_users_profile_expires_at ON users (profile_expires_at)`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("add profile expiry columns: %w", err)
			}
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`DROP INDEX idx_users_profile_expires_at`,
			`ALTER TABLE users DROP COLUMN revoke_key_at_expiry`,
			`ALTER TABLE users DROP COLUMN profile_after_expiry`,
			`ALTER TABLE users DROP COLUMN profile_expires_at`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("drop profile expiry columns: %w", err)
			}
		}
		return nil
	})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/database/ -run "TestExistingUsersHaveNoDeadline|TestDeadlineColumnsRoundTrip" -v`
Expected: PASS, both.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/database/models.go internal/database/migrations/20240009_profile_expiry.go internal/database/profileexpiry_test.go
git commit -m "Add the profile deadline columns

A profile assignment can now carry an end date, a destination and a flag
to delete the key instead. All three are nullable or defaulted, so every
assignment that exists keeps behaving as a permanent one."
```

---

### Task 2: Store methods for deadlines

**Files:**
- Modify: `internal/database/db.go` (extend `SetUserProfile`, add `UsersWithPassedDeadline` and `ApplyProfileExpiry`; amend `DeleteProfile`)
- Test: `internal/database/profileexpiry_test.go` (extend)

**Interfaces:**
- Consumes: the three `User` fields from Task 1.
- Produces:
  - `func (s *Store) SetUserProfileUntil(ctx context.Context, userID int64, profileID *int64, expiresAt *time.Time, afterExpiry *int64, revokeKey bool) error`
  - `func (s *Store) UsersWithPassedDeadline(ctx context.Context, now time.Time) ([]User, error)`
  - `func (s *Store) ApplyProfileExpiry(ctx context.Context, userID int64, newProfileID *int64) error` — sets `profile_id` and clears all three deadline columns in one transaction.
  - `DeleteProfile` additionally nulls `profile_after_expiry` rows pointing at the deleted profile.

Note on naming: the existing `SetUserProfile(ctx, userID, profileID)` stays as it is, used by code that does not care about deadlines. `SetUserProfileUntil` is the superset the admin form calls.

- [ ] **Step 1: Write the failing tests**

Append to `internal/database/profileexpiry_test.go`:

```go
// Only deadlines that have actually passed are returned, or the job would
// revert people early.
func TestUsersWithPassedDeadlineIgnoresFutureAndNull(t *testing.T) {
	s := migratedStore(t, "pexp3")
	ctx := context.Background()
	now := time.Now()

	past, err := s.GetOrCreateUser(ctx, "sub-past", "past@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	future, err := s.GetOrCreateUser(ctx, "sub-future", "future@uni-osnabrueck.de", "F")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrCreateUser(ctx, "sub-none", "none@uni-osnabrueck.de", "N"); err != nil {
		t.Fatal(err)
	}

	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	if err := s.SetUserProfileUntil(ctx, past.ID, nil, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserProfileUntil(ctx, future.ID, nil, &tomorrow, nil, false); err != nil {
		t.Fatal(err)
	}

	due, err := s.UsersWithPassedDeadline(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("got %d users due, want 1: %+v", len(due), due)
	}
	if due[0].ID != past.ID {
		t.Errorf("due user = %d, want the one whose deadline passed (%d)", due[0].ID, past.ID)
	}
}

// Applying an expiry must clear the deadline, or the job would fire again on
// every run for the same user.
func TestApplyProfileExpiryClearsTheDeadline(t *testing.T) {
	s := migratedStore(t, "pexp4")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-c", "c@uni-osnabrueck.de", "C")
	if err != nil {
		t.Fatal(err)
	}
	dest := &Profile{Name: "after"}
	if err := s.CreateProfile(ctx, dest); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := s.SetUserProfileUntil(ctx, u.ID, nil, &yesterday, &dest.ID, true); err != nil {
		t.Fatal(err)
	}

	if err := s.ApplyProfileExpiry(ctx, u.ID, &dest.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID == nil || *got.ProfileID != dest.ID {
		t.Errorf("ProfileID = %v, want the destination %d", got.ProfileID, dest.ID)
	}
	if got.ProfileExpiresAt != nil || got.ProfileAfterExpiry != nil || got.RevokeKeyAtExpiry {
		t.Errorf("deadline not cleared: %v / %v / %v",
			got.ProfileExpiresAt, got.ProfileAfterExpiry, got.RevokeKeyAtExpiry)
	}

	// Idempotence: a second run must find nothing to do.
	due, err := s.UsersWithPassedDeadline(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("got %d users still due after applying, want 0", len(due))
	}
}

// A null destination means the default profile, which is expressed as a null
// profile_id — the user falls back to the default the same way a new user does.
func TestApplyProfileExpiryToDefaultClearsTheProfile(t *testing.T) {
	s := migratedStore(t, "pexp5")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-d", "d@uni-osnabrueck.de", "D")
	if err != nil {
		t.Fatal(err)
	}
	p := &Profile{Name: "temporary"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := s.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	if err := s.ApplyProfileExpiry(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID != nil {
		t.Errorf("ProfileID = %v, want nil so the default profile applies", got.ProfileID)
	}
}

// Deleting a profile that someone was due to land on must not leave a dangling
// reference: those users fall back to the default instead.
func TestDeleteProfileClearsPendingDestinations(t *testing.T) {
	s := migratedStore(t, "pexp6")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-e", "e@uni-osnabrueck.de", "E")
	if err != nil {
		t.Fatal(err)
	}
	dest := &Profile{Name: "doomed"}
	if err := s.CreateProfile(ctx, dest); err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := s.SetUserProfileUntil(ctx, u.ID, nil, &tomorrow, &dest.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProfile(ctx, dest.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileAfterExpiry != nil {
		t.Errorf("ProfileAfterExpiry = %v, want nil after the profile was deleted", got.ProfileAfterExpiry)
	}
	if got.ProfileExpiresAt == nil {
		t.Error("the deadline itself was cleared; only the destination should have been")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/database/ -run "TestUsersWithPassedDeadline|TestApplyProfileExpiry|TestDeleteProfileClearsPending" -v`
Expected: FAIL — `s.SetUserProfileUntil undefined`.

- [ ] **Step 3: Implement the store methods**

Add to `internal/database/db.go`, in the `--- Users ---` section beside `SetUserProfile`:

```go
// SetUserProfileUntil assigns a profile, optionally with a deadline.
//
// A nil expiresAt makes the assignment permanent and clears any deadline that
// was set, so an admin can take a deadline off by clearing the date field. A
// nil afterExpiry means the user falls back to the default profile when the
// deadline passes — it is resolved then rather than now, so the row does not
// go stale if the default changes in between.
func (s *Store) SetUserProfileUntil(ctx context.Context, userID int64, profileID *int64,
	expiresAt *time.Time, afterExpiry *int64, revokeKey bool) error {

	// A deadline is what makes the other two fields meaningful; without one
	// they would sit in the row describing an expiry that never comes.
	if expiresAt == nil {
		afterExpiry, revokeKey = nil, false
	}
	_, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_id = ?", profileID).
		Set("profile_expires_at = ?", expiresAt).
		Set("profile_after_expiry = ?", afterExpiry).
		Set("revoke_key_at_expiry = ?", revokeKey).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).Exec(ctx)
	return err
}

// UsersWithPassedDeadline returns users whose assignment has run out.
//
// Takes the time rather than reading the clock so a test can place a deadline
// either side of it without sleeping.
func (s *Store) UsersWithPassedDeadline(ctx context.Context, now time.Time) ([]User, error) {
	var users []User
	err := s.db.NewSelect().Model(&users).
		Where("profile_expires_at IS NOT NULL AND profile_expires_at <= ?", now).
		OrderExpr("id ASC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return users, nil
}

// ApplyProfileExpiry moves a user to their post-deadline profile and clears the
// deadline.
//
// One transaction: a user left with a passed deadline and the old profile still
// attached would be reverted again on the next run, and a user whose deadline
// was cleared without the profile moving would keep the elevated limits for
// good. A nil newProfileID means the default profile.
func (s *Store) ApplyProfileExpiry(ctx context.Context, userID int64, newProfileID *int64) error {
	_, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_id = ?", newProfileID).
		Set("profile_expires_at = NULL").
		Set("profile_after_expiry = NULL").
		Set("revoke_key_at_expiry = ?", false).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).Exec(ctx)
	return err
}
```

Amend `DeleteProfile` (around line 238) so the existing transaction also clears pending destinations. Add this inside the `RunInTx` closure, before the profile row is deleted:

```go
		// A user due to land on this profile would otherwise keep a dangling
		// reference. They fall back to the default, which is what an unset
		// destination has always meant. The deadline itself stays.
		if _, err := tx.NewUpdate().Model((*User)(nil)).
			Set("profile_after_expiry = NULL").
			Where("profile_after_expiry = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("clear pending profile destinations: %w", err)
		}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/database/ -run "TestUsersWithPassedDeadline|TestApplyProfileExpiry|TestDeleteProfileClearsPending|TestExistingUsers|TestDeadlineColumns" -v`
Expected: PASS, all six.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/db.go internal/database/profileexpiry_test.go
git commit -m "Add store methods for profile deadlines

Applying an expiry moves the profile and clears the deadline in one
statement: a user left with a passed deadline and the old profile would be
reverted again next run, and the reverse would keep the elevated limits for
good.

Deleting a profile clears any pending destination pointing at it rather
than blocking the delete."
```

---

### Task 3: The expiry job

**Files:**
- Create: `internal/profileexpiry/expiry.go`
- Test: `internal/profileexpiry/expiry_test.go`

**Interfaces:**
- Consumes: `UsersWithPassedDeadline`, `ApplyProfileExpiry`, `GetProfile`, `GetDefaultProfile`, `GetAPIKeyByUser`, `DeleteAPIKey`, `RecordAudit` from the store; `keyprovider.Provider`.
- Produces:
  - `type Runner struct` with `func NewRunner(store *database.Store, keys keyprovider.Provider) *Runner`
  - `func (r *Runner) Run(ctx context.Context) error` — one pass, idempotent.
  - `func (r *Runner) Start(ctx context.Context, every time.Duration)` — runs once immediately, then on a ticker until ctx is cancelled. Mirrors `notify.Reminder.Start`.

This lives in its own package, mirroring `internal/notify`, because it is a background job with its own lifecycle rather than a request handler.

- [ ] **Step 1: Write the failing tests**

Create `internal/profileexpiry/expiry_test.go`. This package needs its own store helper and a fake key provider; the repo has `internal/keyprovider/fake.go` — read it first and use it if its shape fits, otherwise write a minimal local fake recording `UpdateLimits` and `DeleteKey` calls.

```go
package profileexpiry

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/database"
)

func testStore(t *testing.T, name string) *database.Store {
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
	if err := store.SeedDefaultProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

// A deadline in the future must be left alone.
func TestRunLeavesFutureDeadlines(t *testing.T) {
	store := testStore(t, "job1")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-1", "a@uni-osnabrueck.de", "A")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &tomorrow, nil, false); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(store, newFakeKeys()).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID == nil || *got.ProfileID != p.ID {
		t.Errorf("ProfileID = %v, want the assignment untouched (%d)", got.ProfileID, p.ID)
	}
}

// The plain case: the deadline passed and no destination was set, so the user
// falls back to the default profile.
func TestRunRevertsToTheDefaultProfile(t *testing.T) {
	store := testStore(t, "job2")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-2", "b@uni-osnabrueck.de", "B")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(store, newFakeKeys()).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID != nil {
		t.Errorf("ProfileID = %v, want nil so the default applies", got.ProfileID)
	}
	if got.ProfileExpiresAt != nil {
		t.Error("the deadline was not cleared")
	}
}

// A destination the admin picked wins over the default.
func TestRunRevertsToTheChosenProfile(t *testing.T) {
	store := testStore(t, "job3")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-3", "c@uni-osnabrueck.de", "C")
	if err != nil {
		t.Fatal(err)
	}
	from := &database.Profile{Name: "raised"}
	to := &database.Profile{Name: "restricted"}
	if err := store.CreateProfile(ctx, from); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(ctx, to); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &from.ID, &yesterday, &to.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(store, newFakeKeys()).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID == nil || *got.ProfileID != to.ID {
		t.Errorf("ProfileID = %v, want the chosen destination %d", got.ProfileID, to.ID)
	}
}

// The new limits must reach the gateway without waiting for a dashboard load,
// or a user who stops visiting keeps the elevated limits on a live key.
func TestRunPushesNewLimitsUpstream(t *testing.T) {
	store := testStore(t, "job4")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-4", "d@uni-osnabrueck.de", "D")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-live", KeyPrefix: "sk-li",
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	fake := newFakeKeys()
	if err := NewRunner(store, fake).Run(ctx); err != nil {
		t.Fatal(err)
	}

	if _, ok := fake.limits["sk-live"]; !ok {
		t.Error("the job did not push the reverted limits to the gateway")
	}
}

// The explicit third option: delete the key rather than switch profiles.
func TestRunRevokesTheKeyWhenAsked(t *testing.T) {
	store := testStore(t, "job5")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-5", "e@uni-osnabrueck.de", "E")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-doomed", KeyPrefix: "sk-do",
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, true); err != nil {
		t.Fatal(err)
	}

	fake := newFakeKeys()
	if err := NewRunner(store, fake).Run(ctx); err != nil {
		t.Fatal(err)
	}

	if !fake.deleted["sk-doomed"] {
		t.Error("the key was not deleted upstream")
	}
	key, err := store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != nil {
		t.Error("the local key row survived the revoke")
	}
}

// Running twice must change nothing the second time.
func TestRunIsIdempotent(t *testing.T) {
	store := testStore(t, "job6")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-6", "f@uni-osnabrueck.de", "F")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, newFakeKeys())
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	if before.UpdatedAt != after.UpdatedAt {
		t.Error("the second run touched a user with no deadline left")
	}
}

// An upstream failure must leave the deadline in place so the next run retries,
// rather than silently dropping the user's limits on the floor.
func TestRunKeepsTheDeadlineWhenTheGatewayFails(t *testing.T) {
	store := testStore(t, "job7")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-7", "g@uni-osnabrueck.de", "G")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-fail", KeyPrefix: "sk-fa",
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, true); err != nil {
		t.Fatal(err)
	}

	fake := newFakeKeys()
	fake.deleteErr = errDeleteFailed
	// The run reports the failure but must not panic or abort the batch.
	_ = NewRunner(store, fake).Run(ctx)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt == nil {
		t.Error("deadline cleared despite the upstream delete failing; the next run cannot retry")
	}
}
```

Add the fake and its error at the top of the file:

```go
var errDeleteFailed = errors.New("gateway unavailable")

// fakeKeys records what the job asked the gateway to do.
type fakeKeys struct {
	limits    map[string]keyprovider.Limits
	deleted   map[string]bool
	deleteErr error
}

func newFakeKeys() *fakeKeys {
	return &fakeKeys{limits: map[string]keyprovider.Limits{}, deleted: map[string]bool{}}
}

func (f *fakeKeys) UpdateLimits(_ context.Context, ref, _ string, l keyprovider.Limits) error {
	f.limits[ref] = l
	return nil
}

func (f *fakeKeys) DeleteKey(_ context.Context, ref string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted[ref] = true
	return nil
}
```

`fakeKeys` must satisfy whatever interface `NewRunner` takes. Keep that interface as narrow as the job actually needs — `UpdateLimits` and `DeleteKey` — and declare it in `expiry.go` rather than taking the whole `keyprovider.Provider`; a narrow interface is what makes this fake two methods instead of ten. Add `errors` and the `keyprovider` import.

If `CreateAPIKey` has a different signature in this repo, read `internal/database/db.go` and adapt these calls — do not change the store to fit the test.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/profileexpiry/ -v`
Expected: FAIL — the package does not exist yet.

- [ ] **Step 3: Implement the runner**

Create `internal/profileexpiry/expiry.go`:

```go
// Package profileexpiry reverts profile assignments whose deadline has passed.
//
// It lives beside the portal rather than inside a request handler because the
// whole point is that it happens without the user doing anything: a user who
// stops visiting the dashboard would otherwise keep elevated limits on a live
// key indefinitely.
package profileexpiry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// gateway is the slice of the key provider this job needs. Narrow on purpose:
// it keeps the test fake to two methods.
type gateway interface {
	UpdateLimits(ctx context.Context, ref, ownerID string, limits keyprovider.Limits) error
	DeleteKey(ctx context.Context, ref string) error
}

// Runner reverts expired profile assignments.
type Runner struct {
	store *database.Store
	keys  gateway
}

func NewRunner(store *database.Store, keys gateway) *Runner {
	return &Runner{store: store, keys: keys}
}

// Run reverts every assignment whose deadline has passed.
//
// Safe to call repeatedly: applying an expiry clears the deadline, so a second
// run finds nothing. One user's failure does not stop the others — a gateway
// blip must not leave the rest of the batch un-reverted.
func (r *Runner) Run(ctx context.Context) error {
	users, err := r.store.UsersWithPassedDeadline(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("find expired assignments: %w", err)
	}
	for i := range users {
		if err := r.expire(ctx, &users[i]); err != nil {
			slog.Error("revert expired profile", "user_id", users[i].ID, "err", err)
		}
	}
	return nil
}

// expire reverts one user.
func (r *Runner) expire(ctx context.Context, u *database.User) error {
	key, err := r.store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("load key: %w", err)
	}

	if u.RevokeKeyAtExpiry {
		return r.revoke(ctx, u, key)
	}

	// Resolve the destination now rather than when the deadline was set: a null
	// destination means "the default profile", whatever that is today.
	var dest *database.Profile
	if u.ProfileAfterExpiry != nil {
		dest, err = r.store.GetProfile(ctx, *u.ProfileAfterExpiry)
		if err != nil {
			return fmt.Errorf("load destination profile: %w", err)
		}
	} else {
		dest, err = r.store.GetDefaultProfile(ctx)
		if err != nil {
			return fmt.Errorf("load default profile: %w", err)
		}
	}

	// Push first, clear the deadline second. The reverse would leave a user
	// with no deadline and a key still carrying the elevated limits, which no
	// later run would ever correct.
	if key != nil {
		if err := r.keys.UpdateLimits(ctx, key.LiteLLMKey, u.OIDCSub, profileLimits(dest)); err != nil {
			return fmt.Errorf("push reverted limits: %w", err)
		}
	}

	if err := r.store.ApplyProfileExpiry(ctx, u.ID, u.ProfileAfterExpiry); err != nil {
		return fmt.Errorf("apply expiry: %w", err)
	}
	r.audit(ctx, u, destName(dest))
	return nil
}

// revoke deletes the key instead of switching profiles.
func (r *Runner) revoke(ctx context.Context, u *database.User, key *database.APIKey) error {
	if key != nil {
		// Upstream first: if that fails the key is still live, so the local row
		// must stay to keep it revocable.
		if err := r.keys.DeleteKey(ctx, key.LiteLLMKey); err != nil {
			return fmt.Errorf("delete key upstream: %w", err)
		}
		if err := r.store.DeleteAPIKey(ctx, key.ID); err != nil {
			return fmt.Errorf("delete local key row: %w", err)
		}
	}
	if err := r.store.ApplyProfileExpiry(ctx, u.ID, u.ProfileAfterExpiry); err != nil {
		return fmt.Errorf("apply expiry: %w", err)
	}
	r.audit(ctx, u, "key deleted")
	return nil
}

func (r *Runner) audit(ctx context.Context, u *database.User, detail string) {
	if err := r.store.RecordAudit(ctx, &database.AuditEvent{
		Action:       database.AuditProfileExpired,
		ActorEmail:   "system", // no admin pressed anything; the deadline fired
		SubjectEmail: u.Email,
		SubjectID:    &u.ID,
		Detail:       detail,
	}); err != nil {
		slog.Error("record profile expiry", "user_id", u.ID, "err", err)
	}
}

func destName(p *database.Profile) string {
	if p == nil {
		return "default"
	}
	return p.Name
}

// profileLimits maps a profile onto the provider-neutral limits.
func profileLimits(p *database.Profile) keyprovider.Limits {
	if p == nil {
		return keyprovider.Limits{}
	}
	windows := make([]keyprovider.QuotaWindow, 0, len(p.Quotas))
	for _, q := range p.Quotas {
		windows = append(windows, keyprovider.QuotaWindow{Budget: q.Budget, Period: q.Period})
	}
	return keyprovider.Limits{
		Models:            p.Models,
		TokensPerMinute:   p.TPMLimit,
		RequestsPerMinute: p.RPMLimit,
		Quotas:            windows,
	}
}

// Start runs the job on an interval until ctx is cancelled.
func (r *Runner) Start(ctx context.Context, every time.Duration) {
	// Run once at startup so a restart does not delay an overdue reversion.
	if err := r.Run(ctx); err != nil {
		slog.Error("profile expiry run", "err", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := r.Run(ctx); err != nil {
				slog.Error("profile expiry run", "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
```

Note: `profileLimits` duplicates the function of the same name in `internal/handlers/ui.go`. That duplication is deliberate for now — the handlers' copy is unexported and moving it is a refactor beyond this task. Flag it in your report; a later task may hoist it into `keyprovider`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/profileexpiry/ -v`
Expected: PASS, all seven.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profileexpiry/
git commit -m "Add the profile expiry job

Reverts assignments whose deadline has passed, pushing the new limits to
the gateway itself rather than waiting for a dashboard load — a user who
stops visiting would otherwise keep the elevated limits on a live key.

Limits are pushed before the deadline is cleared, so an upstream failure
leaves the deadline in place for the next run to retry."
```

---

### Task 4: Wire the job into the server

**Files:**
- Modify: `cmd/server/main.go` (start the runner beside the reminder, around line 203; stop it in the shutdown path)
- Test: none — this is wiring; the job's behaviour is covered by Task 3.

**Interfaces:**
- Consumes: `profileexpiry.NewRunner` and `Start` from Task 3.
- Produces: the job running in the deployed server.

- [ ] **Step 1: Start the runner**

In `cmd/server/main.go`, after the reminder goroutine (around line 203-205), add:

```go
	// Revert expired profile assignments. Every 15 minutes rather than hourly:
	// a deadline an admin set for a particular date should take effect near
	// midnight, not up to an hour into the next day.
	expiryCtx, stopExpiry := context.WithCancel(context.Background())
	go profileexpiry.NewRunner(store, keys).Start(expiryCtx, 15*time.Minute)
```

Add the import `"github.com/virtuos/ai-self-service/internal/profileexpiry"`.

- [ ] **Step 2: Stop it on shutdown**

Find where `stopReminder` is called in the shutdown path and call `stopExpiry()` beside it, so the job stops with the rest of the process rather than being left running during drain.

Read that section before editing — match how `stopReminder` and `stopCleanup` are handled.

- [ ] **Step 3: Verify it builds and the suite passes**

Run: `go build ./...`
Expected: no output.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Verify the job actually runs**

Start the server against the dev OIDC mock and confirm the job runs at startup without error:

```bash
docker compose -f dev/docker-compose.yml --profile mock up -d
```

Then run the server briefly and check the logs show no `profile expiry run` errors. If the dev environment will not start in your sandbox, say so in your report rather than claiming this step passed.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go
git commit -m "Run the profile expiry job

Every 15 minutes rather than hourly: a deadline set for a particular date
should take effect near midnight, not up to an hour into the next day."
```

---

### Task 5: The admin form

**Files:**
- Modify: `internal/handlers/admin.go` (`SetUserProfile`, around line 359; `userRow` struct around line 173)
- Modify: `web/templates/admin.html` (the users table's change-profile form, around line 224-237)
- Modify: `internal/i18n/messages.go` (new keys)
- Test: `internal/handlers/profileexpiry_test.go` (create)

**Interfaces:**
- Consumes: `SetUserProfileUntil` from Task 2.
- Produces: `userRow.ExpiresAt string` (empty when no deadline), `userRow.AfterExpiryID int64` (0 for default), `userRow.RevokeAtExpiry bool` for the template.

- [ ] **Step 1: Write the failing tests**

Create `internal/handlers/profileexpiry_test.go`. Reuse `newGrantTestAdmin` from `internal/handlers/admingrant_test.go` (same package) if its shape fits; otherwise follow its construction.

```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

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
```

`SetUserProfile` reads the user ID from the chi URL parameter, so the tests need a request carrying that route context. Write the helper `postToUser(t, h http.HandlerFunc, rec *httptest.ResponseRecorder, req *http.Request, userID int64)` in this file: it builds a `chi.RouteContext`, sets the `id` URL param to the user ID, attaches it with `context.WithValue(req.Context(), chi.RouteCtxKey, rctx)`, and calls the handler. Read the top of `internal/handlers/admin.go` for the chi import path used in this repo.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/handlers/ -run TestSetUserProfile -v`
Expected: FAIL — the deadline is not stored.

- [ ] **Step 3: Extend the handler**

In `internal/handlers/admin.go`, in `SetUserProfile`, after the existing `profileID` parsing and before the store call, add:

```go
	// An empty date means a permanent assignment. A date that will not parse is
	// refused outright rather than treated as empty: silently dropping it would
	// leave the admin believing a deadline was set.
	var expiresAt *time.Time
	if v := strings.TrimSpace(r.FormValue("expires_at")); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			http.Redirect(w, r, "/admin?flash=Enter+the+date+as+YYYY-MM-DD#users", http.StatusFound)
			return
		}
		// End of the chosen day, so "until 4 October" includes the 4th.
		d = d.Add(24*time.Hour - time.Second)
		expiresAt = &d
	}

	var afterExpiry *int64
	if v := r.FormValue("after_expiry"); v != "" && v != "0" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			afterExpiry = &id
		}
	}
	revokeKey := r.FormValue("revoke_at_expiry") != ""
```

Replace the `a.store.SetUserProfile(...)` call with:

```go
	if err := a.store.SetUserProfileUntil(r.Context(), userID, profileID, expiresAt, afterExpiry, revokeKey); err != nil {
```

Extend the audit detail so the log records the deadline, not just the profile name:

```go
	if expiresAt != nil {
		detail += " until " + expiresAt.Format("2006-01-02")
	}
```

In `userRow` (around line 173), add:

```go
	// ExpiresAt is the deadline as YYYY-MM-DD for the date input, empty when
	// the assignment is permanent.
	ExpiresAt      string
	AfterExpiryID  int64 // 0 means the default profile
	RevokeAtExpiry bool
```

Populate them in `Panel` where the other `userRow` fields are filled.

- [ ] **Step 4: Add the translations**

In `internal/i18n/messages.go`, in the admin group:

```go
	"admin.until":          {DE: "Befristet bis", EN: "Until"},
	"admin.until.help":     {DE: "Leer lassen für eine dauerhafte Zuweisung.", EN: "Leave empty for a permanent assignment."},
	"admin.thenswitch":     {DE: "Danach wechseln zu", EN: "Then switch to"},
	"admin.thenrevoke":     {DE: "Stattdessen Schlüssel löschen", EN: "Delete the key instead"},
	"admin.col.until":      {DE: "Befristung", EN: "Deadline"},
	"admin.permanent":      {DE: "dauerhaft", EN: "permanent"},
```

- [ ] **Step 5: Extend the form**

In `web/templates/admin.html`, in the users table's change-profile form (around line 224-237), add the three controls after the existing profile `<select>`:

```html
                  <input type="date" name="expires_at" value="{{.ExpiresAt}}"
                         title="{{T $.Lang "admin.until.help"}}" style="width:auto">
                  <select name="after_expiry" style="width:auto"
                          title="{{T $.Lang "admin.thenswitch"}}">
                    <option value="0">{{T $.Lang "admin.default"}}</option>
                    {{range $.Profiles}}
                    <option value="{{.ID}}"{{if eq .ID $.AfterExpiryID}} selected{{end}}>{{.Name}}</option>
                    {{end}}
                  </select>
                  <label style="display:flex;gap:.25rem;align-items:center;font-size:.85rem">
                    <input type="checkbox" name="revoke_at_expiry"{{if .RevokeAtExpiry}} checked{{end}}>
                    {{T $.Lang "admin.thenrevoke"}}
                  </label>
```

Careful with the template scope: inside `{{range .Users}}`, `.AfterExpiryID` is the row's field but `$.Profiles` is the page's. The snippet above writes `$.AfterExpiryID` in the range over profiles, which is wrong — the row's value must be captured before that inner range, as the existing code does with `{{$pid := .ProfileIDVal}}` at the top of the row. Add `{{$after := .AfterExpiryID}}` beside it and compare against `$after`.

Add a **Deadline** column to the users table header and a cell showing `{{if .ExpiresAt}}{{.ExpiresAt}}{{else}}<em class="text-faint">{{T $.Lang "admin.permanent"}}</em>{{end}}`, so pending reversions are visible without opening the form. Note the existing empty-state row uses `colspan="5"` while the table already has 6 columns — it will need updating to match the new count.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/handlers/ -v`
Expected: PASS, including the template render and untranslated-prose tests.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/handlers/admin.go web/templates/admin.html internal/i18n/messages.go internal/handlers/profileexpiry_test.go
git commit -m "Let an admin set a deadline on a profile assignment

An empty date means permanent, which is what every existing assignment
stays. A date that will not parse is refused rather than dropped: silently
ignoring it would leave the admin believing a deadline was set."
```

---

### Task 6: Show the deadline to the user

**Files:**
- Modify: `internal/handlers/ui.go` (`dashboardData` around line 82, and `Dashboard`)
- Modify: `web/templates/dashboard.html` (beside the profile name)
- Modify: `internal/i18n/messages.go` (one key)
- Test: `internal/handlers/profileexpiry_test.go` (extend)

**Interfaces:**
- Consumes: `User.ProfileExpiresAt` from Task 1.
- Produces: `dashboardData.ProfileUntil string` — empty when the assignment is permanent.

- [ ] **Step 1: Write the failing test**

Append to `internal/handlers/profileexpiry_test.go`:

```go
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

	if strings.Contains(body, "until") || strings.Contains(body, "bis") {
		t.Error("the dashboard mentions a deadline for a permanent assignment")
	}
}
```

`newTestUI` and `getPage` already exist in this package — see `internal/handlers/profilesync_test.go`. Read their signatures and adapt these calls to match; do not change the helpers.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/handlers/ -run TestDashboard.*Deadline -v`
Expected: FAIL — the date does not appear.

- [ ] **Step 3: Add the field and populate it**

In `internal/handlers/ui.go`, add to `dashboardData` beside `ProfileName`:

```go
	// ProfileUntil is the deadline as YYYY-MM-DD while one is set, empty
	// otherwise. A limit that drops with no warning is a support ticket.
	ProfileUntil string
```

In `Dashboard`, populate it from the user:

```go
	profileUntil := ""
	if su.User.ProfileExpiresAt != nil {
		profileUntil = su.User.ProfileExpiresAt.Format("2006-01-02")
	}
```

and set `ProfileUntil: profileUntil` in the struct literal.

- [ ] **Step 4: Add the translation**

```go
	"dash.profile.until": {DE: "Dieses Profil gilt bis zum %s. Danach gelten wieder die Standardgrenzen.", EN: "This profile applies until %s. The standard limits return afterwards."},
```

- [ ] **Step 5: Show it**

In `web/templates/dashboard.html`, beside where `ProfileName` is rendered, add:

```html
        {{if .ProfileUntil}}
        <p class="text-muted" style="font-size:.85rem;margin-top:.25rem">
          {{printf (T .Lang "dash.profile.until") .ProfileUntil}}
        </p>
        {{end}}
```

Read the surrounding markup first and match its classes and spacing. The `{{printf (T ...) ...}}` form is already used on the admin page for the role sentence — follow that.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/handlers/ -v`
Expected: PASS.

Then: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/handlers/ui.go web/templates/dashboard.html internal/i18n/messages.go internal/handlers/profileexpiry_test.go
git commit -m "Tell the user when their profile expires

A limit that drops with no warning is a support ticket. The dashboard names
the date while a deadline is set and says nothing when there is none."
```

---

### Task 7: Document it

**Files:**
- Modify: `README.md` (Features list, and the Admin panel section's Users bullet)

**Interfaces:**
- Consumes: the finished feature.
- Produces: no code.

- [ ] **Step 1: Add the feature line**

In the Features list, after the profile system bullet:

```markdown
- **Time-limited assignments** — any profile can be assigned to a user until a
  date, after which it reverts on its own to the default profile, to a profile
  the admin chose, or by deleting the key
```

- [ ] **Step 2: Extend the Users bullet**

In the `## Admin panel` section's bulleted tab list, extend the **Users** bullet to mention the deadline:

```markdown
- **Users** — view everyone who has logged in, see their key prefix and expiry,
  assign a profile, and revoke a key. An assignment can carry a deadline: set a
  date and the user reverts to the default profile when it passes, or to a
  profile you choose, or their key is deleted if you tick that instead. Leave
  the date empty for a permanent assignment.
```

Read the surrounding bullets first and match their prose style.

- [ ] **Step 3: Verify**

Run: `go test ./...` and `go build ./...`
Expected: PASS and no output.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "Document time-limited profile assignments"
```

---

## Notes for the executor

- **Nothing here is about one kind of user.** If you find yourself writing `research`, `thesis`, `is_temporary` or any other word naming a particular use in a column, field, label or identifier, stop — the feature is that *any* profile can carry a deadline. The only place a specific word may appear is a test fixture's profile name, where it is arbitrary data.
- **A null destination always means the default profile.** It never means "no access" and never means "delete the key" — deletion is `revoke_key_at_expiry` and nothing else.
- **Push limits before clearing the deadline** (Task 3). The reverse order leaves a user with no deadline and a key still carrying elevated limits, which no later run would correct.
