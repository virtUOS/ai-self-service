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

// newMakeAdminDB returns a migrated database at a fresh path with one account,
// closed again so the command opens it as it would the server's.
func newMakeAdminDB(t *testing.T) (string, *database.User) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := database.NewStore(db)
	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetOrCreateUser(ctx, "sub-a", "a@uni-osnabrueck.de", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	return path, u
}

// The command marks the account, names it in the output, and leaves an entry
// in the audit log like a promotion in the panel.
func TestMakeAdminMarksTheAccount(t *testing.T) {
	path, u := newMakeAdminDB(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := makeAdmin(ctx, path, false, u.ID, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Ada") || !strings.Contains(out.String(), "is now an admin") {
		t.Errorf("output = %q, want it to name the account and the promotion", out.String())
	}

	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := database.NewStore(db)
	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAdmin {
		t.Error("the account is not an admin")
	}
	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != database.AuditAdminGranted ||
		events[0].SubjectEmail != u.Email || events[0].ActorEmail != "command line" {
		t.Errorf("audit events = %+v, want one grant for %s by the command line", events, u.Email)
	}
}

// Running it twice says so instead of writing a second audit entry.
func TestMakeAdminTwice(t *testing.T) {
	path, u := newMakeAdminDB(t)
	ctx := context.Background()
	if err := makeAdmin(ctx, path, false, u.ID, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := makeAdmin(ctx, path, false, u.ID, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already an admin") {
		t.Errorf("output = %q, want it to say the account is already an admin", out.String())
	}

	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := database.NewStore(db).ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("%d audit events, want 1", len(events))
	}
}

// An ID that names no account is an error that says how to find the right one.
func TestMakeAdminUnknownAccount(t *testing.T) {
	path, u := newMakeAdminDB(t)
	err := makeAdmin(context.Background(), path, false, u.ID+1, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "log in once") {
		t.Errorf("err = %v, want a hint that the person has to log in first", err)
	}
}

// With ADMIN_GROUP set, the next login would take the flag away again, so the
// command refuses rather than appear to work.
func TestMakeAdminRefusesWhenTheLoginSystemDecides(t *testing.T) {
	path, u := newMakeAdminDB(t)
	ctx := context.Background()
	err := makeAdmin(ctx, path, true, u.ID, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "ADMIN_GROUP") {
		t.Errorf("err = %v, want a refusal naming ADMIN_GROUP", err)
	}

	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := database.NewStore(db).GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsAdmin {
		t.Error("the account was made an admin anyway")
	}
}

// A mistyped DB_PATH must fail, not create an empty database.
func TestMakeAdminRefusesAMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if err := makeAdmin(context.Background(), path, false, 1, &bytes.Buffer{}); err == nil {
		t.Error("no error for a database that does not exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the command created a database file")
	}
}

// IDs start at 1, so 0 or a negative number is a typo, refused before the
// database is touched.
func TestMakeAdminRefusesAnInvalidID(t *testing.T) {
	path, _ := newMakeAdminDB(t)
	for _, id := range []int64{0, -1} {
		err := makeAdmin(context.Background(), path, false, id, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "not an account ID") {
			t.Errorf("ID %d: err = %v, want a refusal", id, err)
		}
	}
}
