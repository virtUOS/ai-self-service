package database

import (
	"context"
	"testing"
)

// The migration drops the single-quota columns after copying their values
// across. Nothing should still read them, and a profile written afterwards
// must not carry them.
func TestMigrationDropsSingleQuotaColumns(t *testing.T) {
	s := migratedStore(t, "sm1")
	ctx := context.Background()

	// Selecting the dropped column must fail; if it still exists, something
	// is writing to a column the model no longer knows about.
	if err := s.ExecRaw(ctx, `SELECT quota_tokens FROM profiles`); err == nil {
		t.Error("profiles.quota_tokens still exists after the migration")
	}
	if err := s.ExecRaw(ctx, `SELECT quota_period FROM profiles`); err == nil {
		t.Error("profiles.quota_period still exists after the migration")
	}

	// The replacement table is there and usable.
	if err := s.ExecRaw(ctx, `SELECT profile_id, tokens, period FROM profile_quotas`); err != nil {
		t.Errorf("profile_quotas not usable: %v", err)
	}
}

// Deleting a profile must take its windows with it, or orphaned rows
// accumulate and a reused id would inherit someone else's quota.
func TestDeletingProfileRemovesItsQuotas(t *testing.T) {
	s := migratedStore(t, "sm2")
	ctx := context.Background()

	p := &Profile{Name: "temp"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{{Tokens: 1000, Period: "1h"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.QueryRowRaw(ctx, `SELECT COUNT(*) FROM profile_quotas WHERE profile_id = ?`, p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d quota rows orphaned after deleting the profile", n)
	}
}
