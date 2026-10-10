package migrations

import (
	"context"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

var Migrations = migrate.NewMigrations()

// inTx runs a migration step in one transaction, so a step that fails leaves
// the schema as it was. SQLite DDL is transactional. Wrap both up and down:
//
//	Migrations.MustRegister(inTx(up), inTx(down))
//
// MustRegister has to be called from the migration's own file, since bun names
// the migration after it.
//
// Every statement must go through tx. The outer *bun.DB hands out another
// connection, which would wait on this transaction's write lock.
//
// The migrator records the migration only after the transaction commits. A
// crash in between makes the next start run it again; most migrations then
// fail loudly on an object that already exists, rather than being skipped.
func inTx(fn func(ctx context.Context, tx bun.Tx) error) migrate.MigrationFunc {
	return func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, fn)
	}
}
