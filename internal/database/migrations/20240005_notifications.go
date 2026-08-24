package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Tracks which expiry warnings have been sent.
//
// Without this the reminder job would mail the same user on every run. The
// unique index is what actually guarantees at-most-once per (key, threshold):
// two overlapping runs would otherwise both see "not yet sent" and both send.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS expiry_notices (
				id           INTEGER PRIMARY KEY AUTOINCREMENT,
				api_key_id   INTEGER NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
				days_before  INTEGER NOT NULL,
				sent_at      DATETIME NOT NULL
			)
		`); err != nil {
			return fmt.Errorf("create expiry_notices: %w", err)
		}
		if _, err := db.ExecContext(ctx, `
			CREATE UNIQUE INDEX IF NOT EXISTS idx_expiry_notice_once
			ON expiry_notices (api_key_id, days_before)
		`); err != nil {
			return fmt.Errorf("index expiry_notices: %w", err)
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS expiry_notices`)
		return err
	})
}
