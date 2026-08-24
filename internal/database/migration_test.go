package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
)

func testStore(t *testing.T, name string) *Store {
	t.Helper()
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

// A user must not be able to hold two keys once the constraint exists.
func TestUniqueKeyPerUser(t *testing.T) {
	s := testStore(t, "m1")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetOrCreateUser(ctx, "sub1", "a@b.c", "A")
	if err != nil {
		t.Fatal(err)
	}
	k := &APIKey{UserID: u.ID, LiteLLMKey: "sk-1", KeyPrefix: "sk-1", ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.CreateAPIKey(ctx, k); err != nil {
		t.Fatalf("first key: %v", err)
	}
	second := &APIKey{UserID: u.ID, LiteLLMKey: "sk-2", KeyPrefix: "sk-2", ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.CreateAPIKey(ctx, second); err == nil {
		t.Fatal("second key for the same user was accepted; unique constraint missing")
	}
}

// The index is the backstop beneath CreateProfile's demote-then-insert: a raw
// INSERT that bypasses the store must still be refused. (Callers going through
// CreateProfile see the incumbent demoted instead — see
// TestCreateDefaultProfileDemotesPrevious.)
func TestSingleDefaultProfile(t *testing.T) {
	s := testStore(t, "m2")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProfile(ctx, &Profile{Name: "d1", IsDefault: true}); err != nil {
		t.Fatalf("first default: %v", err)
	}
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO profiles (name, is_default, created_at, updated_at) VALUES ('d2', 1, ?, ?)`,
		now, now)
	if err == nil {
		t.Fatal("raw insert of a second default accepted; partial unique index missing")
	}
	// Non-default profiles must still be unrestricted.
	for _, n := range []string{"p1", "p2", "p3"} {
		if err := s.CreateProfile(ctx, &Profile{Name: n}); err != nil {
			t.Fatalf("non-default profile %s rejected: %v", n, err)
		}
	}
}

// A database that already drifted must migrate rather than fail.
func TestMigrationDeduplicatesExistingRows(t *testing.T) {
	s := testStore(t, "m3")
	ctx := context.Background()

	// Create the v1 schema only, so duplicates can be inserted.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '', models TEXT NOT NULL DEFAULT '[]',
			tpm_limit INTEGER, rpm_limit INTEGER, max_budget REAL, budget_duration TEXT,
			is_default INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT, oidc_sub TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', profile_id INTEGER,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL,
			litellm_key TEXT NOT NULL, key_prefix TEXT NOT NULL,
			expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL);
		CREATE TABLE sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL,
			token TEXT NOT NULL UNIQUE, id_token TEXT NOT NULL,
			expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, oidc_sub, email, name, created_at, updated_at)
		  VALUES (1,'s','a@b.c','A',?,?);
		INSERT INTO api_keys (user_id, litellm_key, key_prefix, expires_at, created_at)
		  VALUES (1,'sk-old','sk-old',?,?), (1,'sk-new','sk-new',?,?);
		INSERT INTO profiles (name, is_default, created_at, updated_at)
		  VALUES ('d1',1,?,?), ('d2',1,?,?);
	`, now, now, now, now, now, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}

	// Bun records the init migration as applied, then runs 20240002.
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatalf("migration failed on drifted data: %v", err)
	}

	var keys int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if keys != 1 {
		t.Errorf("api_keys after dedupe = %d, want 1", keys)
	}
	var kept string
	if err := s.db.QueryRowContext(ctx, `SELECT litellm_key FROM api_keys`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != "sk-new" {
		t.Errorf("kept %q, want the newest key sk-new", kept)
	}

	var defaults int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles WHERE is_default <> 0`).Scan(&defaults); err != nil {
		t.Fatal(err)
	}
	if defaults != 1 {
		t.Errorf("default profiles after dedupe = %d, want 1", defaults)
	}
}
