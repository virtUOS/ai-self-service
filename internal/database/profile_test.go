package database

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func migratedStore(t *testing.T, name string) *Store {
	t.Helper()
	s := testStore(t, name)
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func countDefaults(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM profiles WHERE is_default <> 0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Creating a new default must demote the old one rather than be rejected.
func TestCreateDefaultProfileDemotesPrevious(t *testing.T) {
	s := migratedStore(t, "pf1")
	ctx := context.Background()

	if err := s.CreateProfile(ctx, &Profile{Name: "students", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProfile(ctx, &Profile{Name: "lecturers", IsDefault: true}); err != nil {
		t.Fatalf("second default rejected instead of taking over: %v", err)
	}
	if n := countDefaults(t, s); n != 1 {
		t.Fatalf("defaults = %d, want 1", n)
	}
	d, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "lecturers" {
		t.Errorf("default = %q, want lecturers", d.Name)
	}
}

// Promoting an existing profile must likewise demote the incumbent.
func TestUpdateProfileToDefaultDemotesPrevious(t *testing.T) {
	s := migratedStore(t, "pf2")
	ctx := context.Background()

	if err := s.CreateProfile(ctx, &Profile{Name: "students", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	other := &Profile{Name: "lecturers"}
	if err := s.CreateProfile(ctx, other); err != nil {
		t.Fatal(err)
	}

	other.IsDefault = true
	if err := s.UpdateProfile(ctx, other); err != nil {
		t.Fatalf("promoting to default failed: %v", err)
	}
	if n := countDefaults(t, s); n != 1 {
		t.Fatalf("defaults = %d, want 1", n)
	}
	d, _ := s.GetDefaultProfile(ctx)
	if d.Name != "lecturers" {
		t.Errorf("default = %q, want lecturers", d.Name)
	}
}

// Re-saving the current default must not demote itself into a zero-default state.
func TestUpdateDefaultProfileKeepsItself(t *testing.T) {
	s := migratedStore(t, "pf3")
	ctx := context.Background()

	p := &Profile{Name: "students", IsDefault: true, Description: "before"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Description = "after"
	if err := s.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if n := countDefaults(t, s); n != 1 {
		t.Fatalf("defaults = %d, want 1 (profile demoted itself)", n)
	}
}

// Saving the default as not the default would leave none, so it is refused and
// the profile stays the default.
func TestUpdateProfileRefusesToUndefault(t *testing.T) {
	s := migratedStore(t, "pf-undefault")
	ctx := context.Background()

	p := &Profile{Name: "students", IsDefault: true, Description: "before"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.IsDefault = false
	p.Description = "after"
	if err := s.UpdateProfile(ctx, p); !errors.Is(err, ErrDefaultProfile) {
		t.Fatalf("err = %v, want ErrDefaultProfile", err)
	}
	d, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != p.ID || d.Description != "before" {
		t.Errorf("default = %q (%q), want students unchanged", d.Name, d.Description)
	}
}

func TestDeleteProfileRefusesDefault(t *testing.T) {
	s := migratedStore(t, "pf-deldefault")
	ctx := context.Background()

	p := &Profile{Name: "students", IsDefault: true}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{{Budget: 1, Period: "1d"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(ctx, p.ID); !errors.Is(err, ErrDefaultProfile) {
		t.Fatalf("err = %v, want ErrDefaultProfile", err)
	}
	d, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatalf("default profile gone: %v", err)
	}
	if len(d.Quotas) != 1 {
		t.Errorf("quotas = %d, want 1 (refused delete still removed them)", len(d.Quotas))
	}
}

// Deleting a profile users are assigned to is refused with how many there are,
// and the profile keeps its windows.
func TestDeleteProfileRefusesAssignedProfile(t *testing.T) {
	s := migratedStore(t, "pf-delinuse")
	ctx := context.Background()

	p := &Profile{Name: "lecturers"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{{Budget: 1, Period: "1d"}}); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"a", "b"} {
		u, err := s.GetOrCreateUser(ctx, sub, sub+"@example.org", sub)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserProfile(ctx, u.ID, &p.ID); err != nil {
			t.Fatal(err)
		}
	}

	err := s.DeleteProfile(ctx, p.ID)
	var inUse *ProfileInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %v, want ProfileInUseError", err)
	}
	if inUse.Users != 2 {
		t.Errorf("users = %d, want 2", inUse.Users)
	}
	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("profile gone: %v", err)
	}
	if len(got.Quotas) != 1 {
		t.Errorf("quotas = %d, want 1 (refused delete still removed them)", len(got.Quotas))
	}
}

// Models must survive the create/update round trip.
func TestProfileModelsRoundTrip(t *testing.T) {
	s := migratedStore(t, "pf4")
	ctx := context.Background()

	p := &Profile{Name: "restricted", Models: []string{"Qwen/Qwen3.8-27B-FP8", "lernki/gpt-oss-120b"}}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[0] != "Qwen/Qwen3.8-27B-FP8" {
		t.Fatalf("models after create = %#v", got.Models)
	}

	p.Models = []string{"bge-m3"}
	if err := s.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProfile(ctx, p.ID)
	if len(got.Models) != 1 || got.Models[0] != "bge-m3" {
		t.Fatalf("models after update = %#v", got.Models)
	}
}

// New profile fields must round-trip through create and update.
func TestProfileQuotaFieldsPersist(t *testing.T) {
	s := migratedStore(t, "pq1")
	ctx := context.Background()

	p := &Profile{Name: "students", KeyDurationDays: 30}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{
		{Budget: 0.1, Period: "24h"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyDurationDays != 30 {
		t.Fatalf("after create: days=%d", got.KeyDurationDays)
	}
	if len(got.Quotas) != 1 || got.Quotas[0].Budget != 0.1 || got.Quotas[0].Period != "24h" {
		t.Fatalf("after create: quotas=%+v", got.Quotas)
	}

	p.KeyDurationDays = 365
	if err := s.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileQuotas(ctx, p.ID, []ProfileQuota{
		{Budget: 0.5, Period: "30d"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProfile(ctx, p.ID)
	if got.KeyDurationDays != 365 {
		t.Fatalf("after update: days=%d", got.KeyDurationDays)
	}
	if len(got.Quotas) != 1 || got.Quotas[0].Budget != 0.5 || got.Quotas[0].Period != "30d" {
		t.Fatalf("after update: quotas=%+v", got.Quotas)
	}
}

// Profiles created before this migration must keep working, with the new
// columns defaulting to "unset" rather than imposing a zero budget.
func TestExistingProfilesGetSafeDefaults(t *testing.T) {
	s := migratedStore(t, "pq2")
	ctx := context.Background()

	if err := s.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.KeyDurationDays != 0 {
		t.Errorf("KeyDurationDays = %d, want 0 (fall back to server default)", d.KeyDurationDays)
	}
	if len(d.Quotas) != 0 {
		t.Errorf("seeded profile has quotas: %+v", d.Quotas)
	}
}

func TestSeedInsertsIntoEmptyTable(t *testing.T) {
	s := migratedStore(t, "seed-empty")
	ctx := context.Background()

	if err := s.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "default" {
		t.Errorf("default = %q, want default", d.Name)
	}
	if err := s.SeedDefaultProfile(ctx); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if n := countDefaults(t, s); n != 1 {
		t.Errorf("defaults = %d, want 1", n)
	}
}

// A database left without a default must not stop the server from starting
// when a profile named "default" is there to take the role.
func TestSeedPromotesProfileNamedDefault(t *testing.T) {
	s := migratedStore(t, "seed-promote")
	ctx := context.Background()

	p := &Profile{Name: "default", IsDefault: true, Description: "kept"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProfile(ctx, &Profile{Name: "lecturers"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecRaw(ctx, "UPDATE profiles SET is_default = 0"); err != nil {
		t.Fatal(err)
	}

	if err := s.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != p.ID || d.Description != "kept" {
		t.Errorf("default = %d %q, want the existing profile %d promoted", d.ID, d.Description, p.ID)
	}
	var n int
	if err := s.QueryRowRaw(ctx, `SELECT COUNT(*) FROM profiles`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("profiles = %d, want 2 (nothing inserted)", n)
	}
}

// Without a profile named "default", seeding must not add an unlimited one
// that every unassigned user would silently get.
func TestSeedRefusesWithoutCandidate(t *testing.T) {
	s := migratedStore(t, "seed-refuse")
	ctx := context.Background()

	if err := s.CreateProfile(ctx, &Profile{Name: "students", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecRaw(ctx, "UPDATE profiles SET is_default = 0"); err != nil {
		t.Fatal(err)
	}

	err := s.SeedDefaultProfile(ctx)
	if err == nil || !strings.Contains(err.Error(), "no default profile") {
		t.Fatalf("err = %v, want a refusal naming the missing default", err)
	}
	if n := countDefaults(t, s); n != 0 {
		t.Errorf("defaults = %d, want 0", n)
	}
}
