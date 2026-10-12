package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Track the name and email each key's owner was last pushed with, so the
// gateway user follows when a login brings a new name or address.
//
// The limit sync labels each LiteLLM user with the person's name, and with
// their address where LITELLM_USER_EMAIL allows. A key is pending when its
// owner's current name or email differs from what it records here.
//
// Existing keys start with both columns null, so they are all pending: the
// first sync after the upgrade labels every user once.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE api_keys ADD COLUMN synced_owner_name TEXT`,
			`ALTER TABLE api_keys ADD COLUMN synced_owner_email TEXT`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("add synced owner columns: %w", err)
			}
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE api_keys DROP COLUMN synced_owner_email`,
			`ALTER TABLE api_keys DROP COLUMN synced_owner_name`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("drop synced owner columns: %w", err)
			}
		}
		return nil
	}))
}
