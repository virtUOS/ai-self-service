package config

import (
	"slices"
	"testing"
)

// The settings that used to grant the admin panel are no longer read. An
// operator who still sets them must be told, or an upgrade silently leaves
// them with no admins and no idea why.
func TestLoadReportsObsoleteAdminSettings(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_IDS", "sub-1")
	t.Setenv("ADMIN_EMAILS", "")
	t.Setenv("ADMIN_ROLE", "ai-admin")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range cfg.ObsoleteSettings {
		got = append(got, s.Name)
	}
	if !slices.Equal(got, []string{"ADMIN_IDS", "ADMIN_ROLE"}) {
		t.Errorf("ObsoleteSettings = %v, want the two that are set", got)
	}
}

// A clean deployment has nothing to be warned about.
func TestLoadReportsNoObsoleteSettingsWhenUnset(t *testing.T) {
	setRequiredEnv(t)
	for _, s := range obsoleteSettings {
		t.Setenv(s.Name, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ObsoleteSettings) != 0 {
		t.Errorf("ObsoleteSettings = %v, want none", cfg.ObsoleteSettings)
	}
}
