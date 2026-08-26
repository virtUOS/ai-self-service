package handlers

import (
	"strings"
	"testing"
)

// The alias leads with the subject so a key can always be traced to a person,
// and carries the username so LiteLLM's UI stays readable. The email must not
// appear: the IdP can reassign an address, which would leave the gateway
// labelling a key with someone else's.
func TestKeyAliasUsesSubjectAndUsername(t *testing.T) {
	alias, err := keyAlias("7ca16f0b-d201-459e", "rgaritafigue")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(alias, "7ca16f0b-d201-459e-rgaritafigue-") {
		t.Errorf("alias = %q, want subject then username then a suffix", alias)
	}
	if strings.Contains(alias, "@") {
		t.Errorf("alias %q carries an email address", alias)
	}
}

// Rotation creates the replacement while the old key is still live, so two
// aliases built from the same identity must not collide.
func TestKeyAliasIsUniquePerCall(t *testing.T) {
	seen := make(map[string]bool)
	for range 50 {
		alias, err := keyAlias("sub-1", "user")
		if err != nil {
			t.Fatal(err)
		}
		if seen[alias] {
			t.Fatalf("alias %q repeated; a rotation would collide", alias)
		}
		seen[alias] = true
	}
}

// A user whose IdP sends no username still gets a usable alias rather than one
// with an empty segment in the middle.
func TestKeyAliasWithoutUsername(t *testing.T) {
	alias, err := keyAlias("sub-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(alias, "--") {
		t.Errorf("alias = %q, want no empty segment", alias)
	}
	if !strings.HasPrefix(alias, "sub-1-") {
		t.Errorf("alias = %q, want the subject and a suffix", alias)
	}
}

// Usernames arrive from the IdP and can carry spaces, case and punctuation.
func TestSanitiseAlias(t *testing.T) {
	cases := map[string]string{
		"Renato Garita":         "renato-garita",
		"user.name@uni-osna.de": "user-name-uni-osna-de",
		"UPPER":                 "upper",
		"  ":                    "",
		"ok_name-1":             "ok_name-1",
		"???":                   "",
	}
	for in, want := range cases {
		if got := sanitiseAlias(in); got != want {
			t.Errorf("sanitiseAlias(%q) = %q, want %q", in, got, want)
		}
	}
}
