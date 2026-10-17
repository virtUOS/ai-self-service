package config

import "testing"

// The cookies are secure exactly when people reach the portal over HTTPS,
// with no setting to forget. FRONTEND_URL tells: it holds the OIDC callback,
// so with the wrong scheme nobody could log in.
func TestCookieSecureFollowsFrontendURL(t *testing.T) {
	for url, want := range map[string]bool{
		"https://keyosk.example.org":  true,
		"HTTPS://keyosk.example.org":  true,
		"https://keyosk.example.org/": true,
		"http://localhost:8080":       false,
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", url)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", url, err)
		}
		if cfg.CookieSecure != want {
			t.Errorf("%q: CookieSecure = %v, want %v", url, cfg.CookieSecure, want)
		}
	}
}

// The routes, the links in the pages and the __Host- cookies sit at the root
// of the host, so a FRONTEND_URL the portal cannot be served at is refused
// rather than left to fail at the first login.
func TestFrontendURLMustBeTheRootOfAHost(t *testing.T) {
	for _, url := range []string{
		"keyosk.example.org",
		"ftp://keyosk.example.org",
		"https://",
		"https://keyosk.example.org\r",
		"https://example.org/keyosk",
		"https://keyosk.example.org/?x=1",
		"https://keyosk.example.org/#top",
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", url)
		if _, err := Load(); err == nil {
			t.Errorf("%q: Load succeeded, want an error", url)
		}
	}
}

// The OIDC callback is /callback under FRONTEND_URL, with or without its
// trailing slash.
func TestOIDCRedirectURLFollowsFrontendURL(t *testing.T) {
	for frontend, want := range map[string]string{
		"https://keyosk.example.org":  "https://keyosk.example.org/callback",
		"https://keyosk.example.org/": "https://keyosk.example.org/callback",
		"http://localhost:8080":       "http://localhost:8080/callback",
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", frontend)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", frontend, err)
		}
		if got := cfg.OIDCRedirectURL(); got != want {
			t.Errorf("%q: OIDCRedirectURL() = %q, want %q", frontend, got, want)
		}
	}
}

// OIDC_REDIRECT_URL is no longer read; startup warns about it, since a value
// that differed from FRONTEND_URL/callback means the provider has another
// redirect URI registered.
func TestOIDCRedirectURLSettingIsIgnored(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("FRONTEND_URL", "https://keyosk.example.org")
	t.Setenv("OIDC_REDIRECT_URL", "https://other.example.org/cb")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.OIDCRedirectURL(); got != "https://keyosk.example.org/callback" {
		t.Errorf("OIDCRedirectURL() = %q, want the one under FRONTEND_URL", got)
	}
	if len(cfg.ObsoleteSettings) != 1 || cfg.ObsoleteSettings[0].Name != "OIDC_REDIRECT_URL" {
		t.Errorf("ObsoleteSettings = %v, want OIDC_REDIRECT_URL", cfg.ObsoleteSettings)
	}
}

// COOKIE_SECURE is no longer read, whatever it says, in either direction;
// startup warns about it.
func TestCookieSecureSettingIsIgnored(t *testing.T) {
	for frontend, setting := range map[string]string{
		"https://keyosk.example.org": "false",
		"http://localhost:8080":      "true",
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", frontend)
		t.Setenv("COOKIE_SECURE", setting)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if want := setting == "false"; cfg.CookieSecure != want {
			t.Errorf("%s with COOKIE_SECURE=%s: CookieSecure = %v, want %v", frontend, setting, cfg.CookieSecure, want)
		}
		if len(cfg.ObsoleteSettings) != 1 || cfg.ObsoleteSettings[0].Name != "COOKIE_SECURE" {
			t.Errorf("ObsoleteSettings = %v, want COOKIE_SECURE", cfg.ObsoleteSettings)
		}
	}
}
