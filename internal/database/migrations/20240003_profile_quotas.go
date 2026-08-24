package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Moves key expiry onto the profile and replaces the raw dollar budget fields
// with a token quota.
//
// Expiry was a single global env var (KEY_DURATION_DAYS), but student and
// lecturer keys need different lifetimes. key_duration_days = 0 means "use the
// configured default", so existing profiles keep today's behaviour.
//
// Quotas are stored in TOKENS rather than the dollars LiteLLM enforces in.
// Admins reason in tokens; the provider adapter converts using the nominal
// per-token price. Storing dollars would bake today's price into every profile
// and silently change every quota if that price were ever adjusted.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profiles ADD COLUMN key_duration_days INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE profiles ADD COLUMN quota_tokens INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE profiles ADD COLUMN quota_period TEXT NOT NULL DEFAULT ''`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("add profile quota columns: %w", err)
			}
		}

		// Carry over any budget already configured in dollars, converting at the
		// nominal rate so existing limits keep their effective size.
		if _, err := db.ExecContext(ctx, `
			UPDATE profiles
			SET quota_tokens = CAST(max_budget / 0.0000001 AS INTEGER),
			    quota_period = COALESCE(budget_duration, '30d')
			WHERE max_budget IS NOT NULL AND max_budget > 0
		`); err != nil {
			return fmt.Errorf("convert existing budgets to token quotas: %w", err)
		}

		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profiles DROP COLUMN quota_period`,
			`ALTER TABLE profiles DROP COLUMN quota_tokens`,
			`ALTER TABLE profiles DROP COLUMN key_duration_days`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}
