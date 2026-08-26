package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	LiteLLMBaseURL   string
	LiteLLMMasterKey string

	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string

	FrontendURL string

	// AdminIDs are the entries that grant admin rights. Each is either an OIDC
	// subject or an email address; both forms live in one list because an
	// operator setting up a deployment has the address to hand and has to look
	// the subject up.
	//
	// A subject is the durable form: it never changes, so admin rights cannot
	// be silently granted or revoked by the IdP reassigning an address. Email
	// entries are kept working so no deployment breaks on upgrade, but they
	// carry that risk — see IsAdmin.
	AdminIDs []string

	DBPath string

	// SMTPHost enables expiry emails when set (host:port). Without it the
	// portal logs what it would have sent and relies on the dashboard warning.
	SMTPHost     string
	SMTPFrom     string
	SMTPUsername string
	SMTPPassword string

	ListenAddr      string
	CookieSecure    bool
	SessionDuration time.Duration
	KeyDurationDays int
}

func Load() (*Config, error) {
	cfg := &Config{
		LiteLLMBaseURL:   requireEnv("LITELLM_BASE_URL"),
		LiteLLMMasterKey: requireEnv("LITELLM_MASTER_KEY"),

		OIDCIssuerURL:    requireEnv("OIDC_ISSUER_URL"),
		OIDCClientID:     requireEnv("OIDC_CLIENT_ID"),
		OIDCClientSecret: requireEnv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  requireEnv("OIDC_REDIRECT_URL"),

		FrontendURL: requireEnv("FRONTEND_URL"),

		DBPath: envOr("DB_PATH", "./data.db"),

		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPFrom:     envOr("SMTP_FROM", "noreply@uni-osnabrueck.de"),
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),

		ListenAddr: envOr("LISTEN_ADDR", ":8080"),
	}

	// ADMIN_IDS supersedes ADMIN_EMAILS but does not replace it: an existing
	// deployment keeps working untouched, and both are read so an operator can
	// migrate one admin at a time.
	for _, raw := range []string{os.Getenv("ADMIN_EMAILS"), os.Getenv("ADMIN_IDS")} {
		for _, e := range strings.Split(raw, ",") {
			if e = strings.TrimSpace(e); e != "" {
				cfg.AdminIDs = append(cfg.AdminIDs, e)
			}
		}
	}

	secure, err := strconv.ParseBool(envOr("COOKIE_SECURE", "false"))
	if err != nil {
		return nil, fmt.Errorf("COOKIE_SECURE must be true or false: %w", err)
	}
	cfg.CookieSecure = secure

	dur, err := time.ParseDuration(envOr("SESSION_DURATION", "24h"))
	if err != nil {
		return nil, fmt.Errorf("SESSION_DURATION must be a valid duration: %w", err)
	}
	cfg.SessionDuration = dur

	days, err := strconv.Atoi(envOr("KEY_DURATION_DAYS", "90"))
	if err != nil {
		return nil, fmt.Errorf("KEY_DURATION_DAYS must be an integer: %w", err)
	}
	cfg.KeyDurationDays = days

	return cfg, nil
}

// IsAdmin reports whether a user holds admin rights, and whether that was
// decided by their subject or by their email address.
//
// The subject is checked first and is the form to prefer: an email address is
// assigned by the IdP and can be reassigned, so an allowlist keyed on it grants
// rights to whoever holds the address today rather than to a person. Matching
// on it is kept for compatibility, and reported so the caller can say so.
//
// An empty subject never matches, so a user whose IdP omits the claim cannot
// take an admin entry by accident.
func (c *Config) IsAdmin(sub, email string) (admin bool, bySubject bool) {
	for _, a := range c.AdminIDs {
		if sub != "" && a == sub {
			return true, true
		}
	}
	for _, a := range c.AdminIDs {
		if email != "" && strings.EqualFold(a, email) {
			return true, false
		}
	}
	return false, false
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required environment variable %s is not set\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
