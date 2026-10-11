package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun/migrate"

	"github.com/virtuos/ai-self-service/internal/database/migrations"
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
