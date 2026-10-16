package config

import "testing"

// The cookies are secure exactly when people reach the portal over HTTPS,
// with no setting to forget. The redirect URL tells: with the wrong scheme
// nobody could log in.
func TestCookieSecureFollowsRedirectURL(t *testing.T) {
	for url, want := range map[string]bool{
		"https://keyosk.example.org/callback": true,
		"HTTPS://keyosk.example.org/callback": true,
		"http://localhost:8080/callback":      false,
		"keyosk.example.org/callback":         false,
		"https://keyosk.example.org\r":        false,
	} {
		setRequiredEnv(t)
		t.Setenv("OIDC_REDIRECT_URL", url)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", url, err)
		}
		if cfg.CookieSecure != want {
			t.Errorf("%q: CookieSecure = %v, want %v", url, cfg.CookieSecure, want)
		}
	}
}

// The scheme of FRONTEND_URL used to matter for nothing but links, so it may
// well be wrong in a deployment that works; it must not decide.
func TestCookieSecureIgnoresFrontendURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("OIDC_REDIRECT_URL", "https://keyosk.example.org/callback")
	t.Setenv("FRONTEND_URL", "http://keyosk.example.org")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false, want true")
	}
}

// COOKIE_SECURE is no longer read, whatever it says, in either direction;
// startup warns about it.
func TestCookieSecureSettingIsIgnored(t *testing.T) {
	for redirect, setting := range map[string]string{
		"https://keyosk.example.org/callback": "false",
		"http://localhost:8080/callback":      "true",
	} {
		setRequiredEnv(t)
		t.Setenv("OIDC_REDIRECT_URL", redirect)
		t.Setenv("COOKIE_SECURE", setting)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if want := setting == "false"; cfg.CookieSecure != want {
			t.Errorf("%s with COOKIE_SECURE=%s: CookieSecure = %v, want %v", redirect, setting, cfg.CookieSecure, want)
		}
		if len(cfg.ObsoleteSettings) != 1 || cfg.ObsoleteSettings[0].Name != "COOKIE_SECURE" {
			t.Errorf("ObsoleteSettings = %v, want COOKIE_SECURE", cfg.ObsoleteSettings)
		}
	}
}
