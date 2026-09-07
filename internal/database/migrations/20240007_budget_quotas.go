package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Quotas move from tokens to spend budgets.
//
// LiteLLM enforces spend. Storing tokens and converting at one price per
// token was exact only while every model cost the same; with models priced
// differently, no single rate turns spend back into a true token count.
// Admins now configure the budget directly and the gateway gets it as is.
//
// Existing rows convert at the nominal rate every model on this deployment
// was priced at, 0.0000001 per token (README, "How usage limits work"), so
// each enforced cap keeps its size across the upgrade. A deployment that had
// changed that rate should check its profile budgets after upgrading.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profile_quotas ADD COLUMN budget REAL NOT NULL DEFAULT 0`,
			`UPDATE profile_quotas SET budget = tokens * 0.0000001`,
			`ALTER TABLE profile_quotas DROP COLUMN tokens`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("convert token quotas to budgets: %w", err)
			}
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profile_quotas ADD COLUMN tokens INTEGER NOT NULL DEFAULT 0`,
			`UPDATE profile_quotas SET tokens = CAST(budget / 0.0000001 AS INTEGER)`,
			`ALTER TABLE profile_quotas DROP COLUMN budget`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("revert budgets to token quotas: %w", err)
			}
		}
		return nil
	})
}
