package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// A profile assignment can carry a deadline.
//
// All three columns are nullable or defaulted, so every assignment that exists
// keeps behaving as a permanent one. profile_after_expiry is deliberately
// nullable rather than defaulted to a particular profile: null means "the
// default profile", which is resolved when the deadline fires, so the row does
// not go stale if the default changes in between.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE users ADD COLUMN profile_expires_at TIMESTAMP`,
			`ALTER TABLE users ADD COLUMN profile_after_expiry INTEGER`,
			`ALTER TABLE users ADD COLUMN revoke_key_at_expiry BOOLEAN NOT NULL DEFAULT 0`,
			// The job scans for passed deadlines every 15 minutes; without this
			// that is a full table scan each time.
			`CREATE INDEX idx_users_profile_expires_at ON users (profile_expires_at)`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("add profile expiry columns: %w", err)
			}
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`DROP INDEX idx_users_profile_expires_at`,
			`ALTER TABLE users DROP COLUMN revoke_key_at_expiry`,
			`ALTER TABLE users DROP COLUMN profile_after_expiry`,
			`ALTER TABLE users DROP COLUMN profile_expires_at`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("drop profile expiry columns: %w", err)
			}
		}
		return nil
	})
}
