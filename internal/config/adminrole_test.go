package config

import "testing"

// An allowlist has to be edited and redeployed whenever someone joins or
// leaves. A role is granted in the IdP, where staff changes are already
// handled, so the portal stops being a second place to maintain.
func TestIsAdminByRole(t *testing.T) {
	cfg := &Config{AdminRole: "ai-self-service-admin"}

	if !cfg.HasAdminRole([]string{"offline_access", "ai-self-service-admin"}) {
		t.Error("a user holding the configured role was not granted admin")
	}
	if cfg.HasAdminRole([]string{"offline_access", "uma_authorization"}) {
		t.Error("a user without the role was granted admin")
	}
	if cfg.HasAdminRole(nil) {
		t.Error("a user with no roles at all was granted admin")
	}
}

// With no role configured, the claim must never grant admin on its own —
// otherwise a realm that happens to emit roles would hand the panel to anyone.
func TestIsAdminRoleUnsetGrantsNothing(t *testing.T) {
	cfg := &Config{}
	if cfg.HasAdminRole([]string{"admin", "ai-self-service-admin", ""}) {
		t.Error("roles granted admin while ADMIN_ROLE was unset")
	}
}

// Role names are opaque identifiers from the IdP, so they match exactly. Case
// folding could collide two distinct roles.
func TestAdminRoleMatchIsExact(t *testing.T) {
	cfg := &Config{AdminRole: "ai-self-service-admin"}
	if cfg.HasAdminRole([]string{"AI-Self-Service-Admin"}) {
		t.Error("role matched with different case; it must be exact")
	}
}
