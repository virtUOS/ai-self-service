package database

import (
	"context"
	"errors"
	"testing"
)

// twoWindowsForOnePeriod cannot be stored: a profile has one window per period.
// Saving it fails at the quota step, after the profile itself was written.
var twoWindowsForOnePeriod = []ProfileQuota{
	{Budget: 1, Period: "24h"},
	{Budget: 2, Period: "24h"},
}

func newSaveProfileStore(t *testing.T, name string) *Store {
	t.Helper()
	s := testStore(t, name)
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// SaveProfile creates a profile together with its windows, and a later save
// replaces both.
func TestSaveProfileCreatesAndUpdates(t *testing.T) {
	s := newSaveProfileStore(t, "save1")
	ctx := context.Background()

	p := &Profile{Name: "students", Description: "d", Models: []string{"a"}}
	prev, _, err := s.SaveProfile(ctx, p, []ProfileQuota{{Budget: 5, Period: "24h"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Fatal("created profile has no id")
	}
	if prev != nil {
		t.Errorf("create returned a previous profile: %+v", prev)
	}

	p.Description = "changed"
	prev, _, err = s.SaveProfile(ctx, p, []ProfileQuota{{Budget: 2, Period: "1h"}, {Budget: 10, Period: "7d"}})
	if err != nil {
		t.Fatal(err)
	}
	// The update reports the profile as it was, so the audit log can say
	// what changed.
	if prev == nil || prev.Description != "d" ||
		!sameQuotaWindows(prev.Quotas, []ProfileQuota{{Budget: 5, Period: "24h"}}) {
		t.Errorf("previous profile = %+v, want description d and 24h 5", prev)
	}

	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "changed" {
		t.Errorf("description = %q, want %q", got.Description, "changed")
	}
	if !sameQuotaWindows(got.Quotas, []ProfileQuota{{Budget: 2, Period: "1h"}, {Budget: 10, Period: "7d"}}) {
		t.Errorf("quotas = %+v, want 1h 2 and 7d 10", got.Quotas)
	}
}

// A create whose windows cannot be stored leaves no profile behind. One saved
// without its windows would read as unlimited.
func TestSaveProfileCreateRollsBackWhenQuotasFail(t *testing.T) {
	s := newSaveProfileStore(t, "save2")
	ctx := context.Background()

	p := &Profile{Name: "half", Description: "d"}
	if _, _, err := s.SaveProfile(ctx, p, twoWindowsForOnePeriod); err == nil {
		t.Fatal("save with two windows for one period succeeded")
	}

	profiles, err := s.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range profiles {
		if q.Name == "half" {
			t.Fatal("profile was created although its quotas failed")
		}
	}
}

// An update whose windows cannot be stored leaves the profile as it was,
// fields and windows alike.
func TestSaveProfileUpdateRollsBackWhenQuotasFail(t *testing.T) {
	s := newSaveProfileStore(t, "save3")
	ctx := context.Background()

	p := &Profile{Name: "staff", Description: "before", Models: []string{"a"}}
	if _, _, err := s.SaveProfile(ctx, p, []ProfileQuota{{Budget: 5, Period: "24h"}}); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}

	edit := &Profile{ID: p.ID, Name: "staff", Description: "after", Models: []string{"a", "b"}}
	if _, _, err := s.SaveProfile(ctx, edit, twoWindowsForOnePeriod); err == nil {
		t.Fatal("save with two windows for one period succeeded")
	}

	got, err := s.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "before" || len(got.Models) != 1 || got.LimitsRev != before.LimitsRev {
		t.Errorf("profile = %+v, want it unchanged from %+v", got, before)
	}
	if !sameQuotaWindows(got.Quotas, before.Quotas) {
		t.Errorf("quotas = %+v, want %+v", got.Quotas, before.Quotas)
	}
}

// Saving the default as not the default is refused, and the refusal saves
// nothing, not even the windows.
func TestSaveProfileRefusesToUndefaultAndSavesNothing(t *testing.T) {
	s := newSaveProfileStore(t, "save4")
	ctx := context.Background()

	def := &Profile{Name: "default", Description: "before", IsDefault: true,
		Quotas: []ProfileQuota{{Budget: 5, Period: "24h"}}}
	if _, _, err := s.SaveProfile(ctx, def, def.Quotas); err != nil {
		t.Fatal(err)
	}
	edit := &Profile{ID: def.ID, Name: def.Name, Description: "changed"}
	_, _, err := s.SaveProfile(ctx, edit, []ProfileQuota{{Budget: 1, Period: "1h"}})
	if !errors.Is(err, ErrDefaultProfile) {
		t.Fatalf("err = %v, want ErrDefaultProfile", err)
	}

	got, err := s.GetProfile(ctx, def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsDefault || got.Description != def.Description {
		t.Errorf("default profile changed: %+v", got)
	}
	if !sameQuotaWindows(got.Quotas, def.Quotas) {
		t.Errorf("quotas = %+v, want %+v", got.Quotas, def.Quotas)
	}
}
