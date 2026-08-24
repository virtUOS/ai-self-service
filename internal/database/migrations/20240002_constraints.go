package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// This migration makes the database enforce two invariants the application
// only assumed:
//
//  1. a user has at most one API key
//  2. at most one profile is the default
//
// Both were previously maintained by handler code that ignored its own errors,
// so a failed cleanup or two concurrent requests could leave duplicates.
// GetAPIKeyByUser and GetDefaultProfile then silently returned whichever row
// came first — for API keys that means an orphaned key still live in LiteLLM.
//
// Existing rows are de-duplicated before the indexes are created, otherwise
// the migration would fail on any database that already drifted.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// Keep the most recently created key per user; older rows are the
		// orphans a failed rotation left behind.
		if _, err := db.ExecContext(ctx, `
			DELETE FROM api_keys
			WHERE id NOT IN (
				SELECT MAX(id) FROM api_keys GROUP BY user_id
			)
		`); err != nil {
			return fmt.Errorf("dedupe api_keys: %w", err)
		}

		if _, err := db.ExecContext(ctx, `
			CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_user_id
			ON api_keys (user_id)
		`); err != nil {
			return fmt.Errorf("unique index on api_keys.user_id: %w", err)
		}

		// Demote every default but the lowest-id one, so the survivor matches
		// what GetDefaultProfile would previously have picked.
		if _, err := db.ExecContext(ctx, `
			UPDATE profiles SET is_default = 0
			WHERE is_default <> 0
			  AND id <> (SELECT MIN(id) FROM profiles WHERE is_default <> 0)
		`); err != nil {
			return fmt.Errorf("dedupe default profiles: %w", err)
		}

		// A partial index: rows with is_default = 0 are not covered, so any
		// number of non-default profiles remain allowed. Supported by both
		// SQLite and Postgres.
		if _, err := db.ExecContext(ctx, `
			CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_single_default
			ON profiles (is_default) WHERE is_default <> 0
		`); err != nil {
			return fmt.Errorf("unique index on profiles.is_default: %w", err)
		}

		// Session lookups happen on every authenticated request.
		if _, err := db.ExecContext(ctx, `
			CREATE INDEX IF NOT EXISTS idx_sessions_expires_at
			ON sessions (expires_at)
		`); err != nil {
			return fmt.Errorf("index on sessions.expires_at: %w", err)
		}

		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`DROP INDEX IF EXISTS idx_sessions_expires_at`,
			`DROP INDEX IF EXISTS idx_profiles_single_default`,
			`DROP INDEX IF EXISTS idx_api_keys_user_id`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}
