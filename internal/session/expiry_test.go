package session

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/keyosk/internal/database"
)

// newSessionStore opens a migrated in-memory store with one user in it.
func newSessionStore(t *testing.T, name string) (*database.Store, *database.User) {
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
	user, err := store.GetOrCreateUser(ctx, "sub-1", "a@uni-osnabrueck.de", "A")
	if err != nil {
		t.Fatal(err)
	}
	return store, user
}

// A session opens for its user until it expires.
func TestSessionOpensForItsUser(t *testing.T) {
	store, user := newSessionStore(t, "sess1")
	ctx := context.Background()
	m := NewManager(store, time.Hour, false)
	token, err := m.Create(ctx, user.ID, "id-token")
	if err != nil {
		t.Fatal(err)
	}

	su, err := m.Get(ctx, token)
	if err != nil {
		t.Fatalf("fresh session refused: %v", err)
	}
	if su.User.ID != user.ID || su.IDToken != "id-token" {
		t.Errorf("session = user %d, id token %q; want user %d, id-token", su.User.ID, su.IDToken, user.ID)
	}
	if _, err := m.Get(ctx, "not-a-token"); err == nil {
		t.Error("unknown token opened a session")
	}
}

// An expired session is refused at once and its row removed, rather than
// staying usable until the hourly cleanup runs.
func TestExpiredSessionIsRefusedAndRemoved(t *testing.T) {
	store, user := newSessionStore(t, "sess2")
	ctx := context.Background()
	// A negative duration creates a session that has already expired.
	token, err := NewManager(store, -time.Minute, false).Create(ctx, user.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewManager(store, time.Hour, false).Get(ctx, token); err == nil {
		t.Fatal("expired session accepted")
	}
	if _, err := store.GetSessionByToken(ctx, token); err == nil {
		t.Error("expired session row still present after it was refused")
	}
}
