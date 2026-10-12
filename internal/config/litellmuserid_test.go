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
