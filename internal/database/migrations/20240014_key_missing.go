package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Record when the gateway first reported a key as missing.
//
// A key LiteLLM no longer has is marked rather than dropped at once: a wrong
// gateway or a restored database would otherwise make the portal forget every
// key in one run, keys that then come back could no longer be extended or
// revoked. The limit sync unmarks a key that returns and removes one that
// stays missing past the grace period (issue #108).
//
// missing_since is null for a key the gateway has, which is every existing key.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE api_keys ADD COLUMN missing_since TIMESTAMP`); err != nil {
			return fmt.Errorf("add missing_since: %w", err)
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE api_keys DROP COLUMN missing_since`); err != nil {
			return fmt.Errorf("drop missing_since: %w", err)
		}
		return nil
	}))
}
