package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
)

// The command marks every key, including ones that were up to date, and
// reports how many.
func TestResyncLimitsMarksEveryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := database.NewStore(db)
	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	def, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"a", "b"} {
		u, err := store.GetOrCreateUser(ctx, sub, sub+"@uni-osnabrueck.de", sub)
		if err != nil {
			t.Fatal(err)
		}
		k := &database.APIKey{UserID: u.ID, LiteLLMKey: "sk-" + sub, KeyPrefix: "sk-" + sub,
			ExpiresAt: time.Now().Add(time.Hour)}
		if err := store.ReplaceAPIKey(ctx, k); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkLimitsSynced(ctx, k.ID, def.ID, def.LimitsRev); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	var out bytes.Buffer
	if err := resyncLimits(ctx, path, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Marked 2 keys") {
		t.Errorf("output = %q, want it to report 2 keys", out.String())
	}

	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := database.NewStore(db).GetLimitSyncStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 2 {
		t.Errorf("pending = %d, want 2", st.Pending)
	}
}

// A mistyped DB_PATH must fail, not create an empty database and report that
// it marked nothing.
func TestResyncLimitsRefusesAMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if err := resyncLimits(context.Background(), path, &bytes.Buffer{}); err == nil {
		t.Error("no error for a database that does not exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the command created a database file")
	}
}

// A database the server has not migrated yet lacks the sync columns. The
// error should say what to do.
func TestResyncLimitsExplainsAnUnmigratedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE api_keys (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	err = resyncLimits(context.Background(), path, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Errorf("err = %v, want a hint to run the server first", err)
	}
}
