package config

import (
	"slices"
	"testing"
)

// Without ADMIN_GROUP the panel manages admin rights, and the claim path still
// has its default so that setting only the group is enough.
func TestLoadAdminGroupDefaults(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_GROUP", "")
	t.Setenv("ADMIN_GROUP_CLAIM", "")
	t.Setenv("OIDC_EXTRA_SCOPES", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminFromGroups() {
		t.Error("AdminFromGroups() = true with ADMIN_GROUP unset")
	}
	if cfg.AdminGroupClaim != "groups" {
		t.Errorf("AdminGroupClaim = %q, want groups", cfg.AdminGroupClaim)
	}
	if len(cfg.OIDCExtraScopes) != 0 {
		t.Errorf("OIDCExtraScopes = %v, want none", cfg.OIDCExtraScopes)
	}
}

func TestLoadAdminGroup(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_GROUP", " ai-self-service-admin ")
	t.Setenv("ADMIN_GROUP_CLAIM", "realm_access.roles")
	t.Setenv("OIDC_EXTRA_SCOPES", " groups  offline_access ")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AdminFromGroups() || cfg.AdminGroup != "ai-self-service-admin" {
		t.Errorf("AdminGroup = %q, want ai-self-service-admin", cfg.AdminGroup)
	}
	if cfg.AdminGroupClaim != "realm_access.roles" {
		t.Errorf("AdminGroupClaim = %q, want realm_access.roles", cfg.AdminGroupClaim)
	}
	if !slices.Equal(cfg.OIDCExtraScopes, []string{"groups", "offline_access"}) {
		t.Errorf("OIDCExtraScopes = %v", cfg.OIDCExtraScopes)
	}
}

// A claim path with no group to look for would quietly leave admin rights to
// the panel, so it is refused rather than ignored.
func TestLoadRefusesAdminGroupClaimWithoutGroup(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_GROUP", "")
	t.Setenv("ADMIN_GROUP_CLAIM", "groups")

	if _, err := Load(); err == nil {
		t.Error("Load accepted ADMIN_GROUP_CLAIM without ADMIN_GROUP")
	}
}
