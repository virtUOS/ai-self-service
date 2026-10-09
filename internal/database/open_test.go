package database

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/uptrace/bun"
)

// The settings must hold on every connection the pool hands out, not only on
// the one that happened to run first.
func TestOpenConfiguresEveryConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	// Hold two connections at once so the pool has to open a second one.
	for i := 0; i < 2; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		var mode string
		var timeout, fk int
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if mode != "wal" || timeout != 5000 || fk != 1 {
			t.Errorf("connection %d: journal_mode=%s busy_timeout=%d foreign_keys=%d, want wal/5000/1",
				i, mode, timeout, fk)
		}
	}
}

// Transactions that read and then write, run from several goroutines at once,
// must wait for each other rather than fail with "database is locked".
func TestOpenSerializesConcurrentWriters(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "CREATE TABLE counter (n INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO counter (n) VALUES (0)"); err != nil {
		t.Fatal(err)
	}

	const workers, rounds = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				errs <- db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					var n int
					if err := tx.QueryRowContext(ctx, "SELECT n FROM counter").Scan(&n); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, "UPDATE counter SET n = ?", n+1)
					return err
				})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent transaction failed: %v", err)
		}
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT n FROM counter").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != workers*rounds {
		t.Errorf("counter = %d, want %d: an increment was lost", n, workers*rounds)
	}
}
