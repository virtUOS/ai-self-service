package database

import (
	"context"
	"testing"
)

// seedProfileWithQuotas creates a profile carrying two stacked windows.
func seedProfileWithQuotas(t *testing.T, s *Store, name string, isDefault bool) *Profile {
	t.Helper()
	ctx := context.Background()

	p := &Profile{Name: name, Description: "d", IsDefault: isDefault}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{
		{Tokens: 1_000, Period: "1h"},
		{Tokens: 1_000_000, Period: "30d"},
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// The admin table and the edit form both render from ListProfiles, so a
// profile's windows have to come back with it. Without the relation they load
// empty and the panel shows "unlimited" for a profile that has two windows,
// while the edit form opens with no windows to remove.
func TestListProfilesLoadsQuotas(t *testing.T) {
	s := testStore(t, "pq1")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	seedProfileWithQuotas(t, s, "stacked", false)

	profiles, err := s.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var found *Profile
	for i := range profiles {
		if profiles[i].Name == "stacked" {
			found = &profiles[i]
		}
	}
	if found == nil {
		t.Fatal("profile not listed")
	}
	if len(found.Quotas) != 2 {
		t.Fatalf("listed profile carries %d windows, want 2", len(found.Quotas))
	}
}

// Users on the default profile get their limits from GetDefaultProfile. If it
// drops the windows, the portal issues keys that enforce no quota at all —
// a limit that silently does not apply, rather than a display glitch.
func TestGetDefaultProfileLoadsQuotas(t *testing.T) {
	s := testStore(t, "pq2")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	seedProfileWithQuotas(t, s, "the-default", true)

	got, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotas) != 2 {
		t.Fatalf("default profile carries %d windows, want 2", len(got.Quotas))
	}
}

// GetProfile already loaded them; this pins the behaviour so all three paths
// stay consistent.
func TestGetProfileLoadsQuotas(t *testing.T) {
	s := testStore(t, "pq3")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	p := seedProfileWithQuotas(t, s, "byid", false)

	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotas) != 2 {
		t.Fatalf("profile carries %d windows, want 2", len(got.Quotas))
	}
}

// Loading the quota relation adds a join, which must not disturb the ordering
// the admin table relies on: default first, then by name.
func TestListProfilesKeepsOrderingWithQuotas(t *testing.T) {
	s := testStore(t, "pq4")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	seedProfileWithQuotas(t, s, "zulu", false)
	seedProfileWithQuotas(t, s, "alpha", false)
	seedProfileWithQuotas(t, s, "the-default", true)

	profiles, err := s.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(profiles))
	for _, p := range profiles {
		got = append(got, p.Name)
	}
	want := []string{"the-default", "alpha", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A profile with no windows must come back with none rather than being dropped
// from the list by the join.
func TestListProfilesIncludesProfilesWithoutQuotas(t *testing.T) {
	s := testStore(t, "pq5")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProfile(ctx, &Profile{Name: "unlimited", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	seedProfileWithQuotas(t, s, "capped", false)

	profiles, err := s.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for _, p := range profiles {
		byName[p.Name] = len(p.Quotas)
	}
	if _, ok := byName["unlimited"]; !ok {
		t.Error("a profile with no windows was dropped from the list")
	}
	if byName["unlimited"] != 0 {
		t.Errorf("unlimited profile carries %d windows, want none", byName["unlimited"])
	}
	if byName["capped"] != 2 {
		t.Errorf("capped profile carries %d windows, want 2", byName["capped"])
	}
}
