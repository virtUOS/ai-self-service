package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
)

// The number on the button and the number applied by Extend must come from the
// same place, or the UI promises one thing and does another.
func TestExtendLabelMatchesAction(t *testing.T) {
	u := &UI{cfg: &config.Config{KeyDurationDays: 90}}

	// A profile change takes effect on the next extend, for label and action
	// alike — the key's original duration is not stored or consulted.
	students := &database.Profile{KeyDurationDays: 30}
	staff := &database.Profile{KeyDurationDays: 365}

	if got := u.keyDuration(students); got != 30 {
		t.Errorf("students label = %d, want 30", got)
	}
	if got := u.keyDuration(staff); got != 365 {
		t.Errorf("after moving to staff = %d, want 365", got)
	}
	if got := u.keyDuration(nil); got != 90 {
		t.Errorf("no profile = %d, want the server default 90", got)
	}
}

// The button must advertise the date Extend actually sets, not "+N days".
// Extend moves expiry to N days from now, discarding whatever time remained,
// so a key with 60 days left extends to 90 rather than 150. Labelling that as
// an addition promises arithmetic the button does not do. See issue #5.
func TestExtendUntilIsTheDateExtendApplies(t *testing.T) {
	u := &UI{cfg: &config.Config{KeyDurationDays: 90}}
	profile := &database.Profile{KeyDurationDays: 30}

	// What the label advertises.
	label := u.extendUntil(profile)

	// What ExtendKey applies, from the same source.
	applied := time.Now().AddDate(0, 0, u.keyDuration(profile))

	if label != applied.Format("2006-01-02") {
		t.Errorf("label says %q but Extend sets %q", label, applied.Format("2006-01-02"))
	}
}

// The advertised date must not depend on how much time is left on the key,
// because the action does not either.
func TestExtendUntilIgnoresRemainingTime(t *testing.T) {
	u := &UI{cfg: &config.Config{KeyDurationDays: 90}}

	want := time.Now().AddDate(0, 0, 90).Format("2006-01-02")
	if got := u.extendUntil(nil); got != want {
		t.Errorf("extendUntil = %q, want %q", got, want)
	}
}

// The rendered button must show the date in both languages, and must no longer
// claim "+N days".
func TestExtendButtonRendersDateNotDelta(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.DE, i18n.EN} {
		var buf bytes.Buffer
		if err := parseDashboardTemplate().Execute(&buf, dashboardData{
			Lang:        lang,
			User:        &database.User{Name: "T", Email: "t@example.com"},
			APIKey:      &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(24 * time.Hour)},
			ExtendUntil: "2026-11-23",
			CSRFToken:   "TOK",
		}); err != nil {
			t.Fatalf("%s: execute: %v", lang, err)
		}
		out := buf.String()
		if !strings.Contains(out, "2026-11-23") {
			t.Errorf("%s: extend button does not show the resulting date", lang)
		}
		if strings.Contains(out, "(+") {
			t.Errorf("%s: button still advertises an addition", lang)
		}
	}
}
