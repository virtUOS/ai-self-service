package database

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/uptrace/bun/migrate"

	"github.com/virtuos/ai-self-service/internal/database/migrations"
)

// A profile written by the previous release holds its quota in tokens. The
// migration converts at the nominal per-token rate every model was priced at
// (0.0000001), so the enforced cap keeps its size across the upgrade.
func TestMigrationConvertsTokenQuotasToBudgets(t *testing.T) {
	s := testStore(t, "bq1")
	ctx := context.Background()

	// Everything up to, but not including, the budget migration.
	before := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name < "20240007" {
			before.Add(m)
		}
	}
	old := migrate.NewMigrator(s.db, before)
	if err := old.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := s.ExecRaw(ctx, `INSERT INTO profiles (name, is_default, created_at, updated_at) VALUES ('students', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecRaw(ctx, `INSERT INTO profile_quotas (profile_id, tokens, period) VALUES (1, 1500000, '24h')`); err != nil {
		t.Fatal(err)
	}

	// Now the rest, as a real upgrade would run it.
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProfile(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotas) != 1 {
		t.Fatalf("got %d windows, want 1", len(got.Quotas))
	}
	if math.Abs(got.Quotas[0].Budget-0.15) > 1e-9 || got.Quotas[0].Period != "24h" {
		t.Errorf("window = %+v, want 0.15/24h (1.5M tokens at the nominal rate)", got.Quotas[0])
	}
	if err := s.ExecRaw(ctx, `SELECT tokens FROM profile_quotas`); err == nil {
		t.Error("profile_quotas.tokens still exists after the migration")
	}
}
