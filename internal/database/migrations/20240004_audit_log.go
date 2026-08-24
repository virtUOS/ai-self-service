package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Records who issued or revoked which key, and when.
//
// This is the record that answers "why did my key stop working?" — the first
// question an admin gets after revoking one. Rows outlive the key and the user
// they describe, so actor and subject are denormalised: a deleted user must not
// erase the history of an admin acting on their key.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS audit_events (
				id            INTEGER PRIMARY KEY AUTOINCREMENT,
				created_at    DATETIME NOT NULL,
				action        TEXT     NOT NULL,
				actor_email   TEXT     NOT NULL,
				subject_email TEXT     NOT NULL,
				subject_id    INTEGER,
				detail        TEXT     NOT NULL DEFAULT ''
			)
		`); err != nil {
			return fmt.Errorf("create audit_events: %w", err)
		}

		// The panel lists newest-first, and per-user history is the common query.
		for _, stmt := range []string{
			`CREATE INDEX IF NOT EXISTS idx_audit_created_at ON audit_events (created_at DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_audit_subject ON audit_events (subject_id)`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("index audit_events: %w", err)
			}
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS audit_events`)
		return err
	})
}
