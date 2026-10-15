package oidc

import (
	"encoding/json"
	"testing"
)

// Claims are decoded from JSON, so the test builds them the same way: lists
// arrive as []any, objects as map[string]any.
func decodeClaims(t *testing.T, raw string) map[string]any {
	t.Helper()
	var claims map[string]any
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestHasGroup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims string
		path   string
		want   bool
	}{
		{"listed", `{"groups":["staff","ai-admin"]}`, "groups", true},
		{"not listed", `{"groups":["staff"]}`, "groups", false},
		{"single string", `{"groups":"ai-admin"}`, "groups", true},
		{"empty list", `{"groups":[]}`, "groups", false},
		{"claim missing", `{"email":"a@b.c"}`, "groups", false},
		{"nested path", `{"realm_access":{"roles":["ai-admin"]}}`, "realm_access.roles", true},
		{"nested path missing", `{"realm_access":{}}`, "realm_access.roles", false},
		{"path through a non-object", `{"realm_access":["roles"]}`, "realm_access.roles", false},
		{"client roles", `{"resource_access":{"ai-self-service":{"roles":["ai-admin"]}}}`,
			"resource_access.ai-self-service.roles", true},
		// Group names are opaque identifiers: no case folding, and Keycloak's
		// full group path is a different name, not the same group.
		{"case differs", `{"groups":["AI-Admin"]}`, "groups", false},
		{"leading slash", `{"groups":["/ai-admin"]}`, "groups", false},
		{"other shape", `{"groups":{"ai-admin":true}}`, "groups", false},
		{"non-string items", `{"groups":[1,true,null]}`, "groups", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasGroup(decodeClaims(t, tc.claims), tc.path, "ai-admin"); got != tc.want {
				t.Errorf("HasGroup(%s, %q) = %v, want %v", tc.claims, tc.path, got, tc.want)
			}
		})
	}
}
