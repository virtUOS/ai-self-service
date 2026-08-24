package database

import (
	"context"
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
