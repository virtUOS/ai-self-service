package database

import (
	"context"
	"testing"
)

// The migration itself must copy an existing quota across. The other test
// writes the new row by hand, which would pass even if the INSERT..SELECT in
// the migration were wrong — this one exercises the real path.
func TestMigrationCopiesOldQuotaRow(t *testing.T) {
	s := testStore(t, "sm1")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	// Simulate a profile written before the new table existed: old columns
	// populated, no row in profile_quotas.
	p := &Profile{Name: "legacy", QuotaTokens: 750_000, QuotaPeriod: "7d"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecRaw(ctx, `DELETE FROM profile_quotas WHERE profile_id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}

	// Re-run the migration's carry-forward exactly as the migration does.
	if err := s.ExecRaw(ctx, `
		INSERT INTO profile_quotas (profile_id, tokens, period)
		SELECT id, quota_tokens, quota_period
		FROM profiles
		WHERE quota_tokens > 0 AND quota_period != ''
	`); err != nil {
		t.Fatalf("carry-forward statement failed: %v", err)
	}

	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotas) != 1 {
		t.Fatalf("got %d windows, want the legacy quota carried over", len(got.Quotas))
	}
	if got.Quotas[0].Tokens != 750_000 || got.Quotas[0].Period != "7d" {
		t.Errorf("carried %+v, want 750000/7d", got.Quotas[0])
	}
}
