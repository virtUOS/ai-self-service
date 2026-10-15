package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
	"github.com/uptrace/bun/migrate"

	"github.com/virtuos/keyosk/internal/database/migrations"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// migrateBeforeKeyRef runs every migration up to, but not including, the one
// that hashes stored keys.
func migrateBeforeKeyRef(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	before := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name < migrations.KeyRefName {
			before.Add(m)
		}
	}
	old := newMigrator(s.db, before)
	if err := old.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

// The previous release stored each key in plaintext. After the upgrade only
// its hash is left, which is what LiteLLM identifies the key by.
func TestMigrationHashesStoredKeys(t *testing.T) {
	s := testStore(t, "kr1")
	ctx := context.Background()
	migrateBeforeKeyRef(t, s)

	now := time.Now()
	hashed := sha256Hex("sk-already-hashed")
	if err := s.ExecRaw(ctx, `
		INSERT INTO users (id, oidc_sub, email, name, created_at, updated_at)
		  VALUES (1,'s1','a@b.c','A',?,?), (2,'s2','b@b.c','B',?,?);
		INSERT INTO api_keys (user_id, litellm_key, key_prefix, expires_at, created_at)
		  VALUES (1,'sk-plaintext','sk-plaintext',?,?), (2,?,'sk-already',?,?);
	`, now, now, now, now, now, now, hashed, now, now); err != nil {
		t.Fatal(err)
	}

	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	for userID, want := range map[int64]string{1: sha256Hex("sk-plaintext"), 2: hashed} {
		k, err := s.GetAPIKeyByUser(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if k.KeyRef != want {
			t.Errorf("user %d: key_ref = %q, want %q", userID, k.KeyRef, want)
		}
	}
	if err := s.ExecRaw(ctx, `SELECT litellm_key FROM api_keys`); err == nil {
		t.Error("api_keys.litellm_key still exists after the migration")
	}
}

// A TRUNCATE checkpoint cannot finish while another connection still reads an
// older snapshot, and SQLite reports that in the pragma's busy column rather
// than as an error. A scrub stopped there leaves the old pages in the file, so
// it must not pass as done.
func TestScrubReportsABlockedCheckpoint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.db")
	// As Open, but with a short busy timeout: the checkpoint waits that long
	// for the reader before it gives up.
	sqldb, err := sql.Open(sqliteshim.ShimName,
		"file:"+path+"?_journal_mode=WAL&_busy_timeout=50&_txlock=immediate&_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(bun.NewDB(sqldb, sqlitedialect.New()))
	t.Cleanup(func() { s.Close() })
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrCreateUser(ctx, "s1", "a@b.c", "A"); err != nil {
		t.Fatal(err)
	}

	// Another process reading, such as a backup: its open statement holds a
	// snapshot from before the scrub.
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	rows, err := other.QueryContext(ctx, `SELECT id FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("no user to read")
	}

	if err := s.scrub(ctx); err == nil || !strings.Contains(err.Error(), "wal_checkpoint") {
		t.Errorf("scrub with a reader = %v, want the blocked checkpoint reported", err)
	}

	rows.Close()
	if err := s.scrub(ctx); err != nil {
		t.Errorf("scrub once the reader is done = %v, want nil", err)
	}
}

// A hash cannot be turned back into a key, so rolling the migration back must
// fail rather than leave a database the previous release would misread.
func TestKeyRefMigrationCannotBeRolledBack(t *testing.T) {
	s := migratedStore(t, "kr2")
	ctx := context.Background()

	// The whole schema went up as one group, so the rollback starts here.
	_, err := newMigrator(s.db, migrations.Migrations).Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "cannot be turned back") {
		t.Fatalf("rollback = %v, want an error saying it is irreversible", err)
	}
	if err := s.ExecRaw(ctx, `SELECT key_ref FROM api_keys`); err != nil {
		t.Errorf("key_ref gone after a refused rollback: %v", err)
	}
}
