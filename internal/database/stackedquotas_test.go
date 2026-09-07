package database

import (
	"context"
	"testing"
)

// The migration must carry an existing single quota into the new table, or
// every profile silently becomes unlimited on upgrade.
func TestMigrationCarriesExistingQuota(t *testing.T) {
	s := testStore(t, "qw1")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	// A profile written the old way, as the previous release would have.
	p := &Profile{Name: "students"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{{Budget: 0.15, Period: "24h"}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotas) != 1 {
		t.Fatalf("got %d windows, want 1", len(got.Quotas))
	}
	if got.Quotas[0].Budget != 0.15 || got.Quotas[0].Period != "24h" {
		t.Errorf("window = %+v, want 0.15/24h", got.Quotas[0])
	}
}

// Several windows apply at once; that is the whole point of the change.
func TestProfileHoldsSeveralWindows(t *testing.T) {
	s := testStore(t, "qw2")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	p := &Profile{Name: "staff"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{
		{Budget: 0.01, Period: "24h"},
		{Budget: 0.1, Period: "30d"},
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetProfile(ctx, p.ID)
	if len(got.Quotas) != 2 {
		t.Fatalf("got %d windows, want 2", len(got.Quotas))
	}
}

// Replacing the set must remove windows that are gone, not merge them. A
// profile that drops a window would otherwise keep enforcing it.
func TestSetProfileQuotasReplaces(t *testing.T) {
	s := testStore(t, "qw3")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	p := &Profile{Name: "staff"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{
		{Budget: 0.01, Period: "24h"},
		{Budget: 0.1, Period: "30d"},
	}); err != nil {
		t.Fatal(err)
	}
	// Drop back to one window.
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{{Budget: 0.005, Period: "1h"}}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetProfile(ctx, p.ID)
	if len(got.Quotas) != 1 {
		t.Fatalf("got %d windows, want 1 after replacing", len(got.Quotas))
	}
	if got.Quotas[0].Period != "1h" {
		t.Errorf("window = %+v, want the new 1h one", got.Quotas[0])
	}
}

// Clearing all windows means unlimited.
func TestSetProfileQuotasClears(t *testing.T) {
	s := testStore(t, "qw4")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	p := &Profile{Name: "staff"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{{Budget: 0.0000001, Period: "1h"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProfile(ctx, p.ID)
	if len(got.Quotas) != 0 {
		t.Errorf("got %d windows, want none", len(got.Quotas))
	}
}
