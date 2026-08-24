package handlers

import (
	"testing"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
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
