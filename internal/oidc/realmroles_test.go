package oidc

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// token builds an unsigned JWT carrying the given claims. The roles are read
// back out of a token that was already verified when the session was created,
// so signing is not what is under test here.
func token(t *testing.T, claims map[string]any) string {
	t.Helper()
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

// Keycloak puts realm roles under realm_access.roles.
func TestRealmRolesReadsKeycloakShape(t *testing.T) {
	tok := token(t, map[string]any{
		"sub":          "abc",
		"realm_access": map[string]any{"roles": []string{"offline_access", "ai-self-service-admin"}},
	})

	got := RealmRoles(tok)
	if len(got) != 2 || got[1] != "ai-self-service-admin" {
		t.Errorf("roles = %v, want both realm roles", got)
	}
}

// A realm without the mapper simply emits no claim. That must read as "no
// roles" rather than an error, so the caller falls back to the configured list.
func TestRealmRolesAbsentClaimIsEmpty(t *testing.T) {
	tok := token(t, map[string]any{"sub": "abc", "email": "a@b.c"})

	if got := RealmRoles(tok); len(got) != 0 {
		t.Errorf("roles = %v, want none", got)
	}
}

// Malformed input must not panic: the value comes from a stored session, and a
// truncated or rewritten token has to degrade to no roles rather than crash a
// page load.
func TestRealmRolesHandlesJunk(t *testing.T) {
	for _, tok := range []string{
		"", "not-a-token", "a.b", "a.b.c.d",
		"header.!!!not-base64!!!.sig",
		"header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".sig",
	} {
		if got := RealmRoles(tok); len(got) != 0 {
			t.Errorf("RealmRoles(%q) = %v, want none", tok, got)
		}
	}
}

// The roles claim is read from the payload, not the header, so a token whose
// header happens to carry a roles-shaped object grants nothing.
func TestRealmRolesIgnoresTheHeader(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"realm_access":{"roles":["ai-self-service-admin"]}}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"abc"}`))

	if got := RealmRoles(strings.Join([]string{header, payload, "sig"}, ".")); len(got) != 0 {
		t.Errorf("roles = %v, want none — the header is not the payload", got)
	}
}
