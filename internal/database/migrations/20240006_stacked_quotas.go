package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Moves the single per-profile quota into its own table, so a profile can hold
// several windows at once ("100k/day AND 1M/month").
//
// LiteLLM enforces stacked windows on v1.97.0 — a key takes budget_limits as a
// list and applies each independently. An earlier version accepted the field
// and ignored it, which is why the portal only ever offered one period.
//
// profiles.quota_tokens / quota_period are left in place rather than dropped.
// They are no longer read, but keeping them means a rollback to the previous
// release finds its quotas intact rather than silently unlimited.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS profile_quotas (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
				tokens     INTEGER NOT NULL,
				period     TEXT    NOT NULL,
				UNIQUE (profile_id, period)
			)
		`); err != nil {
			return fmt.Errorf("create profile_quotas: %w", err)
		}

		// One row per profile that already had a quota, so behaviour is
		// unchanged until an admin adds a second window.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO profile_quotas (profile_id, tokens, period)
			SELECT id, quota_tokens, quota_period
			FROM profiles
			WHERE quota_tokens > 0 AND quota_period != ''
		`); err != nil {
			return fmt.Errorf("carry quotas into profile_quotas: %w", err)
		}

		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS profile_quotas`)
		return err
	})
}
