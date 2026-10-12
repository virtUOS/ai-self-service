package handlers

import (
	"context"
	"net/url"
	"testing"

	"github.com/virtuos/ai-self-service/internal/database"
)

// auditEvents returns the recorded events, oldest first.
func auditEvents(t *testing.T, store *database.Store) []database.AuditEvent {
	t.Helper()
	events, err := store.ListAuditEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

// checkProfileEvent compares an event against the action, profile name and
// detail it should carry, attributed to the acting admin.
func checkProfileEvent(t *testing.T, e database.AuditEvent, action, name, detail string) {
	t.Helper()
	if e.Action != action || e.ActorEmail != actingAdminEmail || e.SubjectEmail != name ||
		e.SubjectID != nil || e.Detail != detail {
		t.Errorf("event = %+v\nwant %s of %q by %s, detail %q",
			e, action, name, actingAdminEmail, detail)
	}
}

// Creating, changing and deleting a profile each leave an event saying who
// did it and what the profile was or what changed. Saving without a change
// leaves none.
func TestProfileChangesAreAudited(t *testing.T) {
	a, store, token, _ := newSyncTestAdmin(t, "paudit-flow")
	ctx := context.Background()

	postAdminForm(t, a.CreateProfile, token, "/admin/profiles", 0, url.Values{
		"name":              {"students"},
		"model":             {"a", "b"},
		"tpm_limit":         {"1000"},
		"key_duration_days": {"90"},
		"quota_budget":      {"5", "20"},
		"quota_period":      {"24h", "7d"},
	})
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, p := range profiles {
		if p.Name == "students" {
			id = p.ID
		}
	}
	if id == 0 {
		t.Fatal("profile not created")
	}

	edit := url.Values{
		"name":              {"pupils"},
		"model":             {"a", "b", "c"},
		"tpm_limit":         {"1000"},
		"key_duration_days": {"90"},
		"quota_budget":      {"10"},
		"quota_period":      {"24h"},
	}
	postAdminForm(t, a.UpdateProfile, token, "/admin/profiles/1", id, edit)
	postAdminForm(t, a.UpdateProfile, token, "/admin/profiles/1", id, edit)
	postAdminForm(t, a.DeleteProfile, token, "/admin/profiles/1/delete", id, nil)

	events := auditEvents(t, store)
	if len(events) != 3 {
		t.Fatalf("audit = %+v, want created, updated once and deleted", events)
	}
	checkProfileEvent(t, events[0], database.AuditProfileCreated, "students",
		"models a, b; tpm 1000; quotas 24h 5, 7d 20; valid 90d")
	checkProfileEvent(t, events[1], database.AuditProfileUpdated, "pupils",
		"name: students → pupils; models: a, b → a, b, c; quota 24h: 5 → 10; quota 7d: 20 → none")
	checkProfileEvent(t, events[2], database.AuditProfileDeleted, "pupils",
		"models a, b, c; tpm 1000; quotas 24h 10; valid 90d")
}

// A refused delete deleted nothing, so it is not recorded as a deletion.
func TestRefusedProfileDeletesAreNotAudited(t *testing.T) {
	a, store, token, _ := newSyncTestAdmin(t, "paudit-refused")
	ctx := context.Background()
	def, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assigned := &database.Profile{Name: "lecturers"}
	if err := store.CreateProfile(ctx, assigned); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetOrCreateUser(ctx, "sub-l", "l@uni-osnabrueck.de", "L")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProfile(ctx, u.ID, &assigned.ID); err != nil {
		t.Fatal(err)
	}

	postAdminForm(t, a.DeleteProfile, token, "/admin/profiles/1/delete", def.ID, nil)
	postAdminForm(t, a.DeleteProfile, token, "/admin/profiles/1/delete", assigned.ID, nil)

	if events := auditEvents(t, store); len(events) != 0 {
		t.Errorf("audit = %+v, want nothing for refused deletes", events)
	}
}

func TestDescribeProfile(t *testing.T) {
	limit := int64(60)
	tests := []struct {
		name   string
		p      database.Profile
		quotas []database.ProfileQuota
		want   string
	}{
		{"unlimited", database.Profile{}, nil, "models all; quotas unlimited"},
		{
			"everything set",
			database.Profile{Models: []string{"a"}, TPMLimit: &limit, RPMLimit: &limit,
				KeyDurationDays: 30, IsDefault: true},
			[]database.ProfileQuota{{Budget: 20, Period: "30d"}, {Budget: 0.5, Period: "1h"}},
			"models a; tpm 60; rpm 60; quotas 1h 0.5, 30d 20; valid 30d; default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeProfile(&tt.p, tt.quotas); got != tt.want {
				t.Errorf("describeProfile = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiffProfile(t *testing.T) {
	limit := int64(60)
	base := database.Profile{
		Name: "p", Description: "d", Models: []string{"a"}, TPMLimit: &limit,
		Quotas: []database.ProfileQuota{{Budget: 5, Period: "24h"}, {Budget: 1, Period: "1h"}},
	}
	tests := []struct {
		name   string
		edit   func(p *database.Profile)
		quotas []database.ProfileQuota
		want   string
	}{
		{
			"nothing, windows in another order",
			func(*database.Profile) {},
			[]database.ProfileQuota{{Budget: 1, Period: "1h"}, {Budget: 5, Period: "24h"}},
			"",
		},
		{
			"fields",
			func(p *database.Profile) {
				p.Description = "new"
				p.Models = nil
				p.TPMLimit = nil
				p.RPMLimit = &limit
				p.KeyDurationDays = 7
				p.IsDefault = true
			},
			base.Quotas,
			"description changed; models: a → all; tpm: 60 → unlimited; rpm: unlimited → 60; " +
				"valid: server default → 7d; now default",
		},
		{
			"windows added, removed and changed",
			func(*database.Profile) {},
			[]database.ProfileQuota{{Budget: 10, Period: "24h"}, {Budget: 50, Period: "30d"}},
			"quota 1h: 1 → none; quota 24h: 5 → 10; quota 30d: none → 50",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			next.Quotas = nil
			tt.edit(&next)
			if got := diffProfile(&base, &next, tt.quotas); got != tt.want {
				t.Errorf("diffProfile = %q\nwant          %q", got, tt.want)
			}
		})
	}
}
