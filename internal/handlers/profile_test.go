package handlers

import (
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
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
			&database.Profile{Quotas: []database.ProfileQuota{{Tokens: 1_500_000, Period: "24h"}}},
			[]quotaLine{{Tokens: "1.5M", Period: "per day"}},
		},
		{
			&database.Profile{Quotas: []database.ProfileQuota{
				{Tokens: 100_000, Period: "24h"},
				{Tokens: 1_000_000, Period: "30d"},
			}},
			[]quotaLine{
				{Tokens: "100k", Period: "per day"},
				{Tokens: "1M", Period: "per month"},
			},
		},
		// Tokens without a period is not an enforceable window.
		{&database.Profile{Quotas: []database.ProfileQuota{{Tokens: 1000}}}, []quotaLine{}},
	}
	for _, c := range cases {
		got := profileQuotaLines(c.profile, i18n.EN)
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

// profileLimits maps the stored profile onto the provider-neutral limits.
func TestProfileLimits(t *testing.T) {
	tpm := int64(1000)
	p := &database.Profile{
		Models:   []string{"Qwen/Qwen3.8-27B-FP8"},
		TPMLimit: &tpm,
		Quotas: []database.ProfileQuota{
			{Tokens: 1_000_000, Period: "24h"},
		},
	}
	got := profileLimits(p)
	if len(got.Models) != 1 || got.Models[0] != "Qwen/Qwen3.8-27B-FP8" {
		t.Errorf("Models = %#v", got.Models)
	}
	if got.TokensPerMinute == nil || *got.TokensPerMinute != 1000 {
		t.Errorf("TokensPerMinute = %v", got.TokensPerMinute)
	}
	if len(got.Quotas) != 1 || got.Quotas[0].Tokens != 1_000_000 || got.Quotas[0].Period != "24h" {
		t.Errorf("quotas = %+v, want one window of 1M/24h", got.Quotas)
	}
}

// A nil profile must produce empty limits rather than panic.
func TestProfileLimitsNil(t *testing.T) {
	got := profileLimits(nil)
	if len(got.Quotas) != 0 || got.Models != nil {
		t.Errorf("nil profile gave %#v", got)
	}
	_ = keyprovider.Limits{}
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
