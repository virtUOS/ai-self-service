package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Track which limits each key carries upstream, so a change to a profile can
// be pushed to the keys it affects in the background instead of waiting for
// each user to open the dashboard.
//
// profiles.limits_rev goes up whenever a profile's limits change. A key is up
// to date when synced_profile_id and synced_rev match its owner's current
// profile and that profile's revision; anything else is pending.
//
// Existing keys start with both columns null, so they are all pending: the
// first sync after the upgrade pushes every key once. That is deliberate. It
// also moves keys issued before limits were held against their owner onto the
// owner's allowance.
//
// sync_error and sync_failed_at describe the last failed push, for the admin
// panel and so a run does not retry a failing key in a tight loop. A
// successful push clears both.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE profiles ADD COLUMN limits_rev INTEGER NOT NULL DEFAULT 1`,
			`ALTER TABLE api_keys ADD COLUMN synced_profile_id INTEGER`,
			`ALTER TABLE api_keys ADD COLUMN synced_rev INTEGER`,
			`ALTER TABLE api_keys ADD COLUMN sync_error TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE api_keys ADD COLUMN sync_failed_at TIMESTAMP`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("add limit sync columns: %w", err)
			}
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE api_keys DROP COLUMN sync_failed_at`,
			`ALTER TABLE api_keys DROP COLUMN sync_error`,
			`ALTER TABLE api_keys DROP COLUMN synced_rev`,
			`ALTER TABLE api_keys DROP COLUMN synced_profile_id`,
			`ALTER TABLE profiles DROP COLUMN limits_rev`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("drop limit sync columns: %w", err)
			}
		}
		return nil
	}))
}
