package config

import (
	"os"
	"path/filepath"
	"testing"
)

// setRequiredEnv sets everything Load insists on, so a test varies only what
// it is about.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OIDC_ISSUER_URL", "https://example.invalid")
	t.Setenv("OIDC_CLIENT_ID", "id")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	t.Setenv("OIDC_REDIRECT_URL", "https://example.invalid/cb")
	t.Setenv("LITELLM_BASE_URL", "https://example.invalid")
	t.Setenv("LITELLM_MASTER_KEY", "sk-x")
	t.Setenv("FRONTEND_URL", "https://example.invalid")
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The notice is deployment text shipped beside the portal, one file per
// language, and Load hands each file's contents on unchanged.
func TestPrivacyNoticeIsReadFromTheConfiguredFiles(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PRIVACY_NOTICE_FILE_DE", writeFile(t, "de.html", "<h2>Datenschutzhinweise</h2>"))
	t.Setenv("PRIVACY_NOTICE_FILE_EN", writeFile(t, "en.html", "<h2>Privacy notice</h2>"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PrivacyNoticeDE != "<h2>Datenschutzhinweise</h2>" {
		t.Errorf("German notice = %q", cfg.PrivacyNoticeDE)
	}
	if cfg.PrivacyNoticeEN != "<h2>Privacy notice</h2>" {
		t.Errorf("English notice = %q", cfg.PrivacyNoticeEN)
	}
}

// A deployment that sets neither variable has no notice, and that is not an
// error: the portal runs without the page.
func TestPrivacyNoticeIsOptional(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed with no notice configured: %v", err)
	}
	if cfg.PrivacyNoticeDE != "" || cfg.PrivacyNoticeEN != "" {
		t.Errorf("notice present without configuration: %q / %q", cfg.PrivacyNoticeDE, cfg.PrivacyNoticeEN)
	}
}

// A deployment that names a file means to show a notice. A wrong path or an
// empty file must stop the portal starting, the way a missing required
// variable does, rather than quietly drop the page.
func TestPrivacyNoticeFileMustHaveContent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.html")
	for _, tc := range []struct {
		name, env, path string
	}{
		{"German file missing", "PRIVACY_NOTICE_FILE_DE", missing},
		{"English file missing", "PRIVACY_NOTICE_FILE_EN", missing},
		{"German file blank", "PRIVACY_NOTICE_FILE_DE", writeFile(t, "blank.html", " \n\t\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.env, tc.path)

			if _, err := Load(); err == nil {
				t.Errorf("Load accepted %s=%s", tc.env, tc.path)
			}
		})
	}
}
