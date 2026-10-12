package config

import "testing"

// Without a prefix the user is the plain subject, which is what every
// existing deployment's LiteLLM users are named.
func TestLiteLLMUserIDDefaultsToTheSubject(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LiteLLMUserID("3f2a9c1e"); got != "3f2a9c1e" {
		t.Errorf("LiteLLMUserID = %q, want the plain subject", got)
	}
}

func TestLiteLLMUserIDUsesThePrefix(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LITELLM_USER_ID_PREFIX", " testing ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LiteLLMUserID("3f2a9c1e"); got != "testing:3f2a9c1e" {
		t.Errorf("LiteLLMUserID = %q, want testing:3f2a9c1e", got)
	}
}

func TestLiteLLMUserIDPrefixRejectsSeparatorsAndSpaces(t *testing.T) {
	for _, p := range []string{"a:b", "two words", "slash/y", "ümlaut"} {
		t.Run(p, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("LITELLM_USER_ID_PREFIX", p)
			if _, err := Load(); err == nil {
				t.Errorf("Load accepted LITELLM_USER_ID_PREFIX=%q", p)
			}
		})
	}
}

// The email reaches the gateway user only when the option is on: LiteLLM
// refuses an address another user holds, so it must be unique everywhere.
func TestLiteLLMUserCarriesTheEmailOnlyWhenEnabled(t *testing.T) {
	for _, tc := range []struct {
		env, want string
	}{
		{"", ""},
		{"false", ""},
		{"true", "a@uni-osnabrueck.de"},
	} {
		t.Run("LITELLM_USER_EMAIL="+tc.env, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("LITELLM_USER_EMAIL", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			u := cfg.LiteLLMUser("sub-a", "alice", "a@uni-osnabrueck.de")
			if u.ID != "sub-a" || u.Name != "alice" || u.Email != tc.want {
				t.Errorf("user = %+v, want sub-a, alice and email %q", u, tc.want)
			}
		})
	}
}

func TestLiteLLMUserEmailRejectsNonsense(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LITELLM_USER_EMAIL", "sometimes")
	if _, err := Load(); err == nil {
		t.Error("Load accepted LITELLM_USER_EMAIL=sometimes")
	}
}
