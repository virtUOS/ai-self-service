package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	LiteLLMBaseURL  string
	LiteLLMMasterKey string

	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string

	FrontendURL string
	AdminEmails []string

	DBType string
	DBPath string
	DBDSN  string

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

		DBType: envOr("DB_TYPE", "sqlite"),
		DBPath: envOr("DB_PATH", "./data.db"),
		DBDSN:  os.Getenv("DB_DSN"),

		ListenAddr: envOr("LISTEN_ADDR", ":8080"),
	}

	adminRaw := os.Getenv("ADMIN_EMAILS")
	if adminRaw != "" {
		for _, e := range strings.Split(adminRaw, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				cfg.AdminEmails = append(cfg.AdminEmails, e)
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

func (c *Config) IsAdmin(email string) bool {
	for _, a := range c.AdminEmails {
		if strings.EqualFold(a, email) {
			return true
		}
	}
	return false
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
