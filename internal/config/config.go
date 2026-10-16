package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

type Config struct {
	LiteLLMBaseURL   string
	LiteLLMMasterKey string

	// LiteLLMUserIDPrefix sets this portal's people apart from those of other
	// portals on the same gateway. A person's allowance lives on a LiteLLM
	// internal user, so two portals that name their users alike share every
	// budget, and each one's limit sync rewrites the other's (issue #82). Empty
	// keeps the plain OIDC subject, which is what the portal always used, so a
	// single deployment needs no migration. See LiteLLMUserID.
	LiteLLMUserIDPrefix string

	// LiteLLMUserEmail records people's email addresses on their LiteLLM
	// users, which makes them findable in LiteLLM's UI: its user search only
	// matches IDs and addresses. It is off by default because LiteLLM refuses
	// to create a user whose address another user already holds, and its own
	// SSO login matches users by address. Turn it on only where every address
	// is unique across everything connected to the gateway.
	LiteLLMUserEmail bool

	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string

	// OIDCExtraScopes are requested on top of openid, profile and email. Some
	// IdPs (Dex, Okta, Authentik) put group membership in the ID token only
	// when the groups scope is asked for; Keycloak needs nothing here.
	OIDCExtraScopes []string

	FrontendURL string

	// AdminGroup, when set, hands admin rights to the login system: an account
	// is an admin exactly when its ID token lists this group in
	// AdminGroupClaim, re-read at every login, and the panel stops granting
	// and removing them. Empty leaves admin rights to the panel and the
	// database, as recorded on the account (issue #107).
	AdminGroup string
	// AdminGroupClaim is the dot path to the claim that lists the account's
	// groups. It defaults to groups, which most IdPs send; Keycloak realm
	// roles are realm_access.roles.
	AdminGroupClaim string

	// ObsoleteSettings are the settings that are set in the environment but
	// no longer read, so startup can say so and what replaced them.
	ObsoleteSettings []ObsoleteSetting

	DBPath string

	// SMTPHost enables expiry emails when set (host:port). Without it the
	// portal logs what it would have sent and relies on the dashboard warning.
	SMTPHost     string
	SMTPFrom     string
	SMTPUsername string
	SMTPPassword string

	// MetricsToken, when set, is the bearer token /metrics requires. Empty
	// leaves the endpoint open to anyone who can reach it.
	MetricsToken string

	ListenAddr string
	// CookieSecure is true when OIDC_REDIRECT_URL is https. Cookies are then
	// Secure and __Host- prefixed, and responses carry HSTS.
	CookieSecure bool

	SessionDuration time.Duration
	KeyDurationDays int

	// UsageHistoryDays is how far back the dashboard's usage chart and
	// per-model table reach. The gateway's spend-log retention must cover it,
	// or users silently see less history than the page offers.
	UsageHistoryDays int

	// SuccessorURL, when set, marks this portal as being retired: the
	// dashboard tells users to create their key at that address instead and
	// no longer issues or extends keys. Existing keys run out on their own.
	SuccessorURL string

	// BudgetUnit labels quota amounts on the dashboard and admin page. It is
	// the currency LiteLLM prices models in, or a word like "credits" when the
	// prices are nominal and should not read as money.
	BudgetUnit string

	// PrivacyNoticeDE and PrivacyNoticeEN are the deployment's privacy notice,
	// an HTML fragment per language read from the files PRIVACY_NOTICE_FILE_DE
	// and PRIVACY_NOTICE_FILE_EN name. The text is the operator's, not the
	// portal's: it describes what this deployment's gateway and models keep.
	// With neither set the portal has no privacy page.
	PrivacyNoticeDE string
	PrivacyNoticeEN string

	// LimitSyncWorkers is how many pushes the limit sync sends to the gateway
	// at once. One is deliberately gentle: a change to a large profile then
	// takes a while to reach every key, but never floods the gateway.
	LimitSyncWorkers int

	// LimitSyncInterval is how often the limit sync retries keys whose push
	// failed. Admin changes start a run straight away and do not wait for it.
	LimitSyncInterval time.Duration

	// KeyMissingGracePeriod is how long a key the gateway no longer has stays
	// marked before the limit sync removes its record. A gateway that was
	// wrong or restored heals within it.
	KeyMissingGracePeriod time.Duration

	// TrustedProxies are the peers whose X-Forwarded-For is believed when
	// logging the client address. A request from anywhere else is logged with
	// the address that opened the connection, whatever headers it carries.
	//
	// The default is the loopback and private ranges, where a reverse proxy in
	// front of the portal usually sits. Empty trusts nobody.
	TrustedProxies []netip.Prefix
}

// ObsoleteSetting is a setting that is no longer read, with what replaced it.
type ObsoleteSetting struct {
	Name   string
	Reason string
}

// obsoleteSettings are the settings an operator may still set from an older
// version. Admin rights used to come from ADMIN_IDS, ADMIN_EMAILS and
// ADMIN_ROLE; they are recorded on the account now (issue #107), and an
// operator who still sets these would otherwise find nobody is an admin and
// not know why.
var obsoleteSettings = []ObsoleteSetting{
	{"ADMIN_IDS", adminRecordedOnAccount},
	{"ADMIN_EMAILS", adminRecordedOnAccount},
	{"ADMIN_ROLE", adminRecordedOnAccount},
	{"COOKIE_SECURE", "cookies are secure when OIDC_REDIRECT_URL is https"},
}

const adminRecordedOnAccount = "admin rights are recorded on the account, see https://virtuos.github.io/keyosk/administrators/"

// defaultTrustedProxies are the loopback and private ranges, IPv4 and IPv6.
var defaultTrustedProxies = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"fc00::/7",
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

		DBPath: DBPath(),

		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPFrom:     envOr("SMTP_FROM", "noreply@example.com"),
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),

		MetricsToken: strings.TrimSpace(os.Getenv("METRICS_TOKEN")),

		ListenAddr: ListenAddr(),
		BudgetUnit: envOr("BUDGET_UNIT", "$"),

		SuccessorURL: strings.TrimSpace(os.Getenv("SUCCESSOR_URL")),
	}

	prefix := strings.TrimSpace(os.Getenv("LITELLM_USER_ID_PREFIX"))
	if !validUserIDPrefix(prefix) {
		return nil, fmt.Errorf("LITELLM_USER_ID_PREFIX may only contain letters, digits, '-', '_' and '.': %q", prefix)
	}
	cfg.LiteLLMUserIDPrefix = prefix

	userEmail, err := strconv.ParseBool(envOr("LITELLM_USER_EMAIL", "false"))
	if err != nil {
		return nil, fmt.Errorf("LITELLM_USER_EMAIL must be true or false: %w", err)
	}
	cfg.LiteLLMUserEmail = userEmail

	cfg.OIDCExtraScopes = strings.Fields(os.Getenv("OIDC_EXTRA_SCOPES"))

	cfg.AdminGroup = strings.TrimSpace(os.Getenv("ADMIN_GROUP"))
	claim := strings.TrimSpace(os.Getenv("ADMIN_GROUP_CLAIM"))
	if claim != "" && cfg.AdminGroup == "" {
		// A claim with no group to look for would silently leave admin rights
		// to the panel, which is not what someone setting it expects.
		return nil, fmt.Errorf("ADMIN_GROUP_CLAIM is set but ADMIN_GROUP is not")
	}
	if claim == "" {
		claim = "groups"
	}
	cfg.AdminGroupClaim = claim

	for _, s := range obsoleteSettings {
		if strings.TrimSpace(os.Getenv(s.Name)) != "" {
			cfg.ObsoleteSettings = append(cfg.ObsoleteSettings, s)
		}
	}

	// The cookies and HSTS depend on what the browser sees, also when a
	// reverse proxy in front ends TLS. The redirect URL is the one address
	// whose scheme is known to be right: with a wrong one, nobody could log
	// in. A URL that does not parse breaks the login as well.
	redirect, err := url.Parse(cfg.OIDCRedirectURL)
	cfg.CookieSecure = err == nil && redirect.Scheme == "https"

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

	workers, err := strconv.Atoi(envOr("LIMIT_SYNC_WORKERS", "1"))
	if err != nil || workers < 1 {
		return nil, fmt.Errorf("LIMIT_SYNC_WORKERS must be a positive integer")
	}
	cfg.LimitSyncWorkers = workers

	interval, err := time.ParseDuration(envOr("LIMIT_SYNC_INTERVAL", "5m"))
	if err != nil || interval <= 0 {
		return nil, fmt.Errorf("LIMIT_SYNC_INTERVAL must be a positive duration such as 5m")
	}
	cfg.LimitSyncInterval = interval

	grace, err := time.ParseDuration(envOr("KEY_MISSING_GRACE_PERIOD", "24h"))
	if err != nil || grace <= 0 {
		return nil, fmt.Errorf("KEY_MISSING_GRACE_PERIOD must be a positive duration such as 24h")
	}
	cfg.KeyMissingGracePeriod = grace

	proxies, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return nil, err
	}
	cfg.TrustedProxies = proxies

	for _, n := range []struct {
		env string
		dst *string
	}{
		{"PRIVACY_NOTICE_FILE_DE", &cfg.PrivacyNoticeDE},
		{"PRIVACY_NOTICE_FILE_EN", &cfg.PrivacyNoticeEN},
	} {
		text, err := readNotice(strings.TrimSpace(os.Getenv(n.env)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.env, err)
		}
		*n.dst = text
	}

	return cfg, nil
}

// parseTrustedProxies reads a comma-separated list of CIDRs or single IPs.
// Unset falls back to the private ranges; "none" trusts nobody.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "none") {
		return nil, nil
	}
	entries := strings.Split(raw, ",")
	if raw == "" {
		entries = defaultTrustedProxies
	}
	var prefixes []netip.Prefix
	for _, e := range entries {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		p, err := netip.ParsePrefix(e)
		if err != nil {
			// A single address is a prefix of its full length. One with a
			// zone is refused: a zoned peer never matches a prefix, so the
			// entry would silently trust nobody.
			if a, aerr := netip.ParseAddr(e); aerr == nil && a.Zone() == "" {
				a = a.Unmap()
				p = netip.PrefixFrom(a, a.BitLen())
			}
		}
		if !p.IsValid() {
			return nil, fmt.Errorf("TRUSTED_PROXIES must be a comma-separated list of CIDRs or IPs: %q", e)
		}
		prefixes = append(prefixes, p.Masked())
	}
	return prefixes, nil
}

// validUserIDPrefix reports whether a prefix is safe to put in front of a
// subject. The separator, ':', is excluded so the prefix always ends where the
// subject starts.
func validUserIDPrefix(p string) bool {
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// LiteLLMUserID is the LiteLLM internal user that holds a person's allowance.
// It is the one place that ID is built: the key, the limit sync and the
// dashboard must all name the same user.
//
// The ID is derived, never stored, so changing the prefix of a portal that
// already has keys leaves those keys on the old users until each is pushed
// again. -resync-limits does that for all of them.
func (c *Config) LiteLLMUserID(sub string) string {
	if c.LiteLLMUserIDPrefix == "" {
		return sub
	}
	return c.LiteLLMUserIDPrefix + ":" + sub
}

// LiteLLMUser is the LiteLLM internal user a person's keys are bound to: its
// ID (see LiteLLMUserID), the name it is labelled with, and the email address
// when LITELLM_USER_EMAIL allows recording it.
func (c *Config) LiteLLMUser(sub, name, email string) keyprovider.User {
	u := keyprovider.User{ID: c.LiteLLMUserID(sub), Name: name}
	if c.LiteLLMUserEmail {
		u.Email = email
	}
	return u
}

// readNotice reads a privacy notice file, or nothing when no path is given.
//
// A named file that is missing or blank is an error rather than no notice: the
// operator asked for the page, and a typo in the path would otherwise remove it
// without anyone noticing.
// AdminFromGroups reports whether admin rights come from the login system's
// group claim rather than from the panel.
func (c *Config) AdminFromGroups() bool { return c.AdminGroup != "" }

func readNotice(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return string(b), nil
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required environment variable %s is not set\n", key)
		os.Exit(1)
	}
	return v
}

// DBPath is the SQLite database file. The -resync-limits command reads it
// without loading the rest of the configuration.
func DBPath() string {
	return envOr("DB_PATH", "./data.db")
}

// ListenAddr is the address the server listens on. The -healthcheck mode
// reads it without loading the rest of the configuration.
func ListenAddr() string {
	return envOr("LISTEN_ADDR", ":8080")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
