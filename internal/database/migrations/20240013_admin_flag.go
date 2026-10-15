package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// AdminFlagName is this migration's name, for tests that need the schema as
// it was before it.
const AdminFlagName = "20240013"

// Admin rights move onto the account.
//
// Until now the panel could be reached three ways: an IdP role, an entry in
// the configuration (a subject or an email address) and a grant made in the
// panel, by email. An address is assigned by the IdP and can be reassigned, so
// deciding admin rights by it hands the panel to whoever holds the address
// today (issue #107). From here on one flag on the account decides, set by
// another admin in the panel or by the operator with one command.
//
// Grants that had been anchored to a subject on the person's first login are
// carried over to that account. A grant by email whose person never logged in
// has no account to attach to and is dropped: they can be promoted once they
// have.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT 0`,
			`UPDATE users SET is_admin = 1
			   WHERE oidc_sub IN (SELECT oidc_sub FROM admin_grants WHERE oidc_sub IS NOT NULL)`,
			`DROP TABLE admin_grants`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("move admin rights onto users: %w", err)
			}
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		// The previous release reads grants from this table, so give it the
		// flagged accounts back as grants it can resolve.
		for _, stmt := range []string{
			`CREATE TABLE admin_grants (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				oidc_sub TEXT,
				email TEXT NOT NULL COLLATE NOCASE UNIQUE,
				granted_by_email TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL
			)`,
			`INSERT OR IGNORE INTO admin_grants (oidc_sub, email, granted_by_email, created_at)
			   SELECT oidc_sub, email, 'rollback', updated_at FROM users WHERE is_admin <> 0`,
			`ALTER TABLE users DROP COLUMN is_admin`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("move admin rights back to grants: %w", err)
			}
		}
		return nil
	}))
}
