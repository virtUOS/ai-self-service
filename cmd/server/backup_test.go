package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/database"
)

// newBackupDB returns a migrated database with one account. The connection
// stays open, as the server's would.
func newBackupDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)
	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateUser(ctx, "sub-a", "a@uni-osnabrueck.de", "Ada"); err != nil {
		t.Fatal(err)
	}
	return path
}

// The backup is a complete database, holds what was committed, and is
// readable only by its owner.
func TestBackupDatabase(t *testing.T) {
	path := newBackupDB(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "backup.db")

	var out bytes.Buffer
	if err := backupDatabase(ctx, path, target, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), target) {
		t.Errorf("output = %q, want it to name %s", out.String(), target)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: the backup holds personal data", perm)
	}
	// Checked before opening the backup, which adds its own -wal and -shm.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want only the backup", len(entries))
	}

	db, err := database.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity_check = %q, %v", check, err)
	}
	if _, err := database.NewStore(db).GetUserByOIDCSub(ctx, "sub-a"); err != nil {
		t.Errorf("the account is missing from the backup: %v", err)
	}
}

// A write the server has not committed yet neither blocks the backup nor
// ends up in it.
func TestBackupDatabaseWhileWriting(t *testing.T) {
	path := newBackupDB(t)
	ctx := context.Background()

	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE users SET name = 'uncommitted'"); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "backup.db")
	if err := backupDatabase(ctx, path, target, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	bdb, err := database.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer bdb.Close()
	u, err := database.NewStore(bdb).GetUserByOIDCSub(ctx, "sub-a")
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Ada" {
		t.Errorf("name = %q, want the committed Ada", u.Name)
	}
}

// An existing backup is replaced, so a cron job can write to the same file
// every time.
func TestBackupDatabaseReplacesAnExistingBackup(t *testing.T) {
	path := newBackupDB(t)
	target := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(target, []byte("old backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := backupDatabase(context.Background(), path, target, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "SQLite format 3") {
		t.Errorf("the old backup was not replaced: %q", b[:min(len(b), 20)])
	}
}

// Replacing the database or a file SQLite keeps next to it would destroy it.
func TestBackupDatabaseRefusesTheDatabaseFiles(t *testing.T) {
	path := newBackupDB(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, link, path + "-wal", path + "-shm", path + "-journal", filepath.Dir(path)} {
		if err := backupDatabase(context.Background(), path, target, &bytes.Buffer{}); err == nil {
			t.Errorf("no error for %s", target)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the database changed")
	}
}

// A mistyped DB_PATH must fail, not create an empty database and back it up.
func TestBackupDatabaseRefusesAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.db")
	target := filepath.Join(dir, "backup.db")
	if err := backupDatabase(context.Background(), path, target, &bytes.Buffer{}); err == nil {
		t.Error("no error for a database that does not exist")
	}
	for _, p := range []string{path, target} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the command created %s", p)
		}
	}
}

// A backup that fails leaves no temporary file behind.
func TestBackupDatabaseCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if err := backupDatabase(context.Background(), path, filepath.Join(outDir, "backup.db"), &bytes.Buffer{}); err == nil {
		t.Error("no error for a file that is not a database")
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Errorf("left %d files behind", len(entries))
	}
}
