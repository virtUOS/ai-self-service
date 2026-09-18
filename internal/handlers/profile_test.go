package handlers

import (
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
)

// Expiry comes from the profile when set, and from the server otherwise, so
// students and lecturers can have different key lifetimes.
func TestKeyDurationPrefersProfile(t *testing.T) {
	u := &UI{cfg: &config.Config{KeyDurationDays: 90}}

	cases := []struct {
		name    string
		profile *database.Profile
		want    int
	}{
		{"nil profile falls back", nil, 90},
		{"unset profile falls back", &database.Profile{}, 90},
		{"students override", &database.Profile{KeyDurationDays: 30}, 30},
		{"lecturers override", &database.Profile{KeyDurationDays: 365}, 365},
	}
	for _, c := range cases {
		if got := u.keyDuration(c.profile); got != c.want {
			t.Errorf("%s: keyDuration = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestDashboardQuotaRendering(t *testing.T) {
	cases := []struct {
		profile *database.Profile
		want    []quotaLine
	}{
		{nil, nil},
		{&database.Profile{}, []quotaLine{}},
		{
			&database.Profile{Quotas: []database.ProfileQuota{{Budget: 0.15, Period: "24h"}}},
			[]quotaLine{{Budget: "$0.15", Period: "per day"}},
		},
		{
			&database.Profile{Quotas: []database.ProfileQuota{
				{Budget: 0.01, Period: "24h"},
				{Budget: 0.1, Period: "30d"},
			}},
			[]quotaLine{
				{Budget: "$0.01", Period: "per day"},
				{Budget: "$0.10", Period: "per month"},
			},
		},
		// A budget without a period is not an enforceable window.
		{&database.Profile{Quotas: []database.ProfileQuota{{Budget: 0.1}}}, []quotaLine{}},
	}
	for _, c := range cases {
		got := profileQuotaLines(c.profile, i18n.EN, "$")
		if len(got) != len(c.want) {
			t.Errorf("profileQuotaLines(%v) = %+v, want %+v", c.profile, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("window %d = %+v, want %+v", i, got[i], c.want[i])
			}
		}
	}
}

// The dashboard warning must escalate as expiry approaches and agree with the
// widest email threshold, so the two channels do not contradict each other.
func TestExpiryWarningThresholds(t *testing.T) {
	cases := []struct {
		name   string
		in     time.Duration
		days   int
		urgent bool
	}{
		{"no key", 0, 0, false},
		{"fresh 90d", 90*24*time.Hour + time.Minute, 90, false},
		{"just outside window", 15*24*time.Hour + time.Minute, 15, false},
		{"inside window", 13*24*time.Hour + time.Minute, 13, true},
		{"tomorrow", 25*time.Hour + time.Minute, 1, true},
		{"expired", -48 * time.Hour, -2, true},
	}
	for _, c := range cases {
		var k *database.APIKey
		if c.name != "no key" {
			k = &database.APIKey{ExpiresAt: time.Now().Add(c.in)}
		}
		if got := daysUntilExpiry(k); got != c.days {
			t.Errorf("%s: days = %d, want %d", c.name, got, c.days)
		}
		if got := isExpiryUrgent(k); got != c.urgent {
			t.Errorf("%s: urgent = %v, want %v", c.name, got, c.urgent)
		}
	}
}
