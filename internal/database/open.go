package database

import (
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
)

// Open opens the SQLite database at path, configured for a web server whose
// handlers and background jobs write concurrently.
//
// Every option is a DSN parameter rather than a one-off PRAGMA, so it applies
// to each connection the pool opens, not just the first:
//
//   - _journal_mode=WAL lets readers proceed while one connection writes, and
//     lets a second process (an admin command) write alongside the server.
//   - _busy_timeout=5000 makes a connection wait up to 5s for a lock instead
//     of failing at once with "database is locked".
//   - _txlock=immediate starts transactions with BEGIN IMMEDIATE. A deferred
//     transaction that reads first and writes later cannot wait for the write
//     lock: SQLite fails it with SQLITE_BUSY regardless of the busy timeout.
//     Taking the lock up front makes it wait like any other write.
//   - _foreign_keys=on enforces REFERENCES and ON DELETE CASCADE, which SQLite
//     ignores on a connection that has not enabled them.
//
// There is deliberately no cache=shared: shared-cache mode uses table locks
// that fail with SQLITE_LOCKED, which the busy timeout does not retry.
func Open(path string) (*bun.DB, error) {
	dsn := "file:" + path +
		"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate&_foreign_keys=on"
	sqldb, err := sql.Open(sqliteshim.ShimName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return bun.NewDB(sqldb, sqlitedialect.New()), nil
}
