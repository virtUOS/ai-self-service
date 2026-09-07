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

	// AdminRole is a role from the IdP that grants the admin panel. Set it and
	// admin membership is managed where staff changes are already handled,
	// rather than in a list this repo has to keep in step.
	//
	// Empty disables role checking entirely: a realm that emits roles must not
	// hand out the panel until an operator has named the one that counts.
	AdminRole string

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

	// UsageHistoryDays is how far back the dashboard's usage chart and
	// per-model table reach. The gateway's spend-log retention must cover it,
	// or users silently see less history than the page offers.
	UsageHistoryDays int

	// SuccessorURL, when set, marks this portal as being retired: the
	// dashboard tells users to create their key at that address instead and
	// that keys issued here will be revoked. SuccessorKeysRevokedOn is the
	// date to name for that, shown as given; empty leaves the date out.
	SuccessorURL           string
	SuccessorKeysRevokedOn string

	// BudgetUnit labels quota amounts on the dashboard and admin page. It is
	// the currency LiteLLM prices models in, or a word like "credits" when the
	// prices are nominal and should not read as money.
	BudgetUnit string
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
		BudgetUnit: envOr("BUDGET_UNIT", "$"),

		SuccessorURL:           strings.TrimSpace(os.Getenv("SUCCESSOR_URL")),
		SuccessorKeysRevokedOn: strings.TrimSpace(os.Getenv("SUCCESSOR_KEYS_REVOKED_ON")),
	}

	cfg.AdminRole = strings.TrimSpace(os.Getenv("ADMIN_ROLE"))

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

	history, err := strconv.Atoi(envOr("USAGE_HISTORY_DAYS", "30"))
	if err != nil || history < 1 {
		return nil, fmt.Errorf("USAGE_HISTORY_DAYS must be a positive integer")
	}
	cfg.UsageHistoryDays = history

	days, err := strconv.Atoi(envOr("KEY_DURATION_DAYS", "90"))
	if err != nil {
		return nil, fmt.Errorf("KEY_DURATION_DAYS must be an integer: %w", err)
	}
	cfg.KeyDurationDays = days

	return cfg, nil
}

// HasAdminRole reports whether any of the roles a user's token carries is the
// one configured to grant the admin panel.
//
// Matching is exact: a role name is an opaque identifier from the IdP, and
// folding case could collide two roles the realm considers distinct.
func (c *Config) HasAdminRole(roles []string) bool {
	if c.AdminRole == "" {
		return false
	}
	for _, r := range roles {
		if r == c.AdminRole {
			return true
		}
	}
	return false
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
