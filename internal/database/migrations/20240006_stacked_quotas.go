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
// The single-quota columns are dropped once their values have been carried
// across: nothing is in production yet, so there is no deployment to roll back
// to that would still need them.
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

		for _, stmt := range []string{
			`ALTER TABLE profiles DROP COLUMN quota_tokens`,
			`ALTER TABLE profiles DROP COLUMN quota_period`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("drop single-quota columns: %w", err)
			}
		}

		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profiles ADD COLUMN quota_tokens INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE profiles ADD COLUMN quota_period TEXT NOT NULL DEFAULT ''`,
			`DROP TABLE IF EXISTS profile_quotas`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}
