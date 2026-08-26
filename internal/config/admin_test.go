package config

import "testing"

// Admin rights should follow the person, not an address the IdP can reassign.
// A subject entry is authoritative and reported as such, so an operator can
// tell which of their entries are still on the fragile form.
func TestIsAdminPrefersSubject(t *testing.T) {
	cfg := &Config{AdminIDs: []string{"sub-123", "boss@uni-osnabrueck.de"}}

	admin, bySubject := cfg.IsAdmin("sub-123", "someone.else@uni-osnabrueck.de")
	if !admin || !bySubject {
		t.Errorf("subject entry: admin=%v bySubject=%v, want both true", admin, bySubject)
	}

	// The legacy form still works, and is reported as not-by-subject.
	admin, bySubject = cfg.IsAdmin("sub-999", "boss@uni-osnabrueck.de")
	if !admin || bySubject {
		t.Errorf("email entry: admin=%v bySubject=%v, want true/false", admin, bySubject)
	}

	if admin, _ := cfg.IsAdmin("nobody", "nobody@uni-osnabrueck.de"); admin {
		t.Error("granted admin to an identity on neither list")
	}
}

// An IdP that omits the subject claim must not let a user match an entry by
// having an empty one — every non-admin would otherwise match a blank entry.
func TestIsAdminIgnoresEmptyIdentifiers(t *testing.T) {
	cfg := &Config{AdminIDs: []string{"sub-123", "boss@uni-osnabrueck.de"}}

	if admin, _ := cfg.IsAdmin("", ""); admin {
		t.Error("granted admin to a user with no identifiers at all")
	}
	// A blank entry in the allowlist must not match a user with no subject.
	blank := &Config{AdminIDs: []string{""}}
	if admin, _ := blank.IsAdmin("", "a@b.c"); admin {
		t.Error("a blank allowlist entry granted admin")
	}
}

// Email matching stays case-insensitive, as addresses are.
func TestIsAdminEmailIsCaseInsensitive(t *testing.T) {
	cfg := &Config{AdminIDs: []string{"Boss@Uni-Osnabrueck.DE"}}
	if admin, _ := cfg.IsAdmin("sub-1", "boss@uni-osnabrueck.de"); !admin {
		t.Error("email comparison is case-sensitive")
	}
}

// A subject is opaque and must match exactly: case-folding it could collide
// two distinct users on IdPs that issue case-sensitive identifiers.
func TestIsAdminSubjectMatchIsExact(t *testing.T) {
	cfg := &Config{AdminIDs: []string{"SUB-abc"}}
	if admin, bySubject := cfg.IsAdmin("sub-abc", "x@y.z"); admin && bySubject {
		t.Error("subject matched with different case; it must be exact")
	}
}

// The point of issue #31: a user's email can change, and admin rights must not
// change with it. With the subject listed, the same person keeps admin under a
// new address — and the address alone no longer carries the rights to whoever
// is assigned it next.
func TestAdminSurvivesAnEmailChange(t *testing.T) {
	cfg := &Config{AdminIDs: []string{"sub-stable"}}

	before, _ := cfg.IsAdmin("sub-stable", "old.name@uni-osnabrueck.de")
	after, _ := cfg.IsAdmin("sub-stable", "new.name@uni-osnabrueck.de")
	if !before || !after {
		t.Errorf("admin before=%v after=%v, want it to survive the rename", before, after)
	}

	// And the freed address grants nothing to whoever inherits it.
	if inherited, _ := cfg.IsAdmin("sub-other", "old.name@uni-osnabrueck.de"); inherited {
		t.Error("the old address still grants admin to a different person")
	}
}

// An existing deployment sets ADMIN_EMAILS and must keep working untouched
// after the upgrade, and a deployment setting both must get the union so an
// operator can migrate one admin at a time.
func TestAdminIDsReadBothEnvVars(t *testing.T) {
	t.Setenv("ADMIN_EMAILS", "legacy@uni-osnabrueck.de")
	t.Setenv("ADMIN_IDS", "sub-new, sub-other")
	// Everything else Load requires.
	t.Setenv("OIDC_ISSUER_URL", "https://example.invalid")
	t.Setenv("OIDC_CLIENT_ID", "id")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	t.Setenv("OIDC_REDIRECT_URL", "https://example.invalid/cb")
	t.Setenv("LITELLM_BASE_URL", "https://example.invalid")
	t.Setenv("LITELLM_MASTER_KEY", "sk-x")
	t.Setenv("SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("FRONTEND_URL", "https://example.invalid")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if admin, bySubject := cfg.IsAdmin("sub-new", "x@y.z"); !admin || !bySubject {
		t.Error("ADMIN_IDS subject entry does not grant admin")
	}
	if admin, bySubject := cfg.IsAdmin("sub-unknown", "legacy@uni-osnabrueck.de"); !admin || bySubject {
		t.Error("existing ADMIN_EMAILS entry stopped working after the upgrade")
	}
	// Whitespace around a comma-separated entry must not defeat the match.
	if admin, _ := cfg.IsAdmin("sub-other", "x@y.z"); !admin {
		t.Error("entry with surrounding whitespace was not trimmed")
	}
}
