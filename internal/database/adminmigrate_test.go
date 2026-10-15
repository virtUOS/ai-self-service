package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/uptrace/bun/migrate"

	"github.com/virtuos/keyosk/internal/database/migrations"
)

// migrateBeforeAdminFlag runs every migration up to, but not including, the
// one that moves admin rights onto the account.
func migrateBeforeAdminFlag(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	before := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name < migrations.AdminFlagName {
			before.Add(m)
		}
	}
	old := newMigrator(s.db, before)
	if err := old.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

// seedOldGrants writes the previous release's state: two accounts, a grant
// anchored to the first one's subject, and a grant by email for someone who
// never logged in.
func seedOldGrants(t *testing.T, s *Store) {
	t.Helper()
	now := time.Now()
	if err := s.ExecRaw(context.Background(), `
		INSERT INTO users (id, oidc_sub, email, name, created_at, updated_at)
		  VALUES (1,'s1','a@b.c','A',?,?), (2,'s2','b@b.c','B',?,?);
		INSERT INTO admin_grants (oidc_sub, email, granted_by_email, created_at)
		  VALUES ('s1','a@b.c','boss',?), (NULL,'never@b.c','boss',?);
	`, now, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}
}

// A grant made in the panel under the previous release was anchored to the
// person's subject on their first login. The upgrade must carry it onto their
// account, or every panel-granted admin is locked out by the update. A grant
// by email alone names nobody the portal knows and is dropped.
func TestMigrationCarriesSubjectLinkedGrants(t *testing.T) {
	s := testStore(t, "adminmig1")
	ctx := context.Background()
	migrateBeforeAdminFlag(t, s)
	seedOldGrants(t, s)

	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	admins, err := s.ListAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 || admins[0].ID != 1 {
		t.Errorf("admins after the upgrade = %+v, want only account 1", admins)
	}
	if err := s.ExecRaw(ctx, `SELECT 1 FROM admin_grants`); err == nil {
		t.Error("admin_grants still exists after the migration")
	}
}

// Rolling the migration back gives the previous release its grants table,
// holding every flagged account, so it can still resolve them.
func TestAdminFlagMigrationRollsBack(t *testing.T) {
	s := testStore(t, "adminmig2")
	ctx := context.Background()
	migrateBeforeAdminFlag(t, s)
	seedOldGrants(t, s)
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := newMigrator(s.db, migrations.Migrations).Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var subs []string
	if err := s.db.NewRaw(`SELECT oidc_sub FROM admin_grants ORDER BY id`).Scan(ctx, &subs); err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0] != "s1" {
		t.Errorf("grants after the rollback = %v, want the flagged account's subject", subs)
	}
	if err := s.ExecRaw(ctx, `SELECT is_admin FROM users`); err == nil {
		t.Error("users.is_admin still exists after the rollback")
	}
}

// Promoting an id that names no account must fail, so a stale form cannot
// report a promotion that did not happen.
func TestSetUserAdminRefusesAMissingAccount(t *testing.T) {
	s := migratedStore(t, "adminmig3")
	err := s.SetUserAdmin(context.Background(), 404, true)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}
