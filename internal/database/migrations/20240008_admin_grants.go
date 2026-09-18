package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Admin rights become grantable from the admin panel.
//
// ADMIN_ROLE and ADMIN_IDS keep working and keep their precedence; this table
// only adds admins on top of them. The env list stays a floor the panel cannot
// remove, so no sequence of clicks can lock everyone out of the panel.
//
// oidc_sub is nullable because an admin can be named by email before they have
// ever logged in; it is filled in on their first login and is what authorises
// them thereafter.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE admin_grants (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				oidc_sub TEXT,
				email TEXT NOT NULL COLLATE NOCASE UNIQUE,
				granted_by_email TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL
			)`); err != nil {
			return fmt.Errorf("create admin_grants: %w", err)
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		if _, err := db.ExecContext(ctx, `DROP TABLE admin_grants`); err != nil {
			return fmt.Errorf("drop admin_grants: %w", err)
		}
		return nil
	})
}
