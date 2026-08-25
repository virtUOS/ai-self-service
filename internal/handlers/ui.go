package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
	"github.com/virtuos/ai-self-service/internal/litellm"
	"github.com/virtuos/ai-self-service/internal/metrics"
	oidcpkg "github.com/virtuos/ai-self-service/internal/oidc"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

type UI struct {
	cfg      *config.Config
	store    *database.Store
	sessions *session.Manager
	oidc     *oidcpkg.Provider
	keys     keyprovider.Provider
	tmpl     *template.Template
	flash    *keyFlash
	csrf     *session.CSRF
	models   *modelCache
	usage    *usageCache
}

func NewUI(cfg *config.Config, store *database.Store, sessions *session.Manager, oidc *oidcpkg.Provider, keys keyprovider.Provider, csrf *session.CSRF) *UI {
	tmpl := parseDashboardTemplate()
	// Only some gateways can enumerate models; the dashboard omits the list
	// when the provider cannot.
	lister, _ := keys.(keyprovider.ModelLister)
	reporter, _ := keys.(keyprovider.UsageReporter)
	return &UI{cfg: cfg, store: store, sessions: sessions, oidc: oidc, keys: keys,
		tmpl: tmpl, flash: newKeyFlash(), csrf: csrf,
		models: newModelCache(lister), usage: newUsageCache(reporter)}
}

func (u *UI) requireSession(r *http.Request) (*session.SessionUser, error) {
	token := u.sessions.TokenFromRequest(r)
	if token == "" {
		return nil, http.ErrNoCookie
	}
	return u.sessions.Get(r.Context(), token)
}

// parseDashboardTemplate builds the dashboard template with its helpers.
// Tests use it too, so a helper added here cannot be missed there.
func parseDashboardTemplate() *template.Template {
	return template.Must(template.New("dashboard.html").
		Funcs(langFuncs()).
		ParseFS(web.TemplateFS, "templates/dashboard.html"))
}

// dashboardData is what dashboard.html renders. Named rather than anonymous so
// tests cannot drift from the handler's shape.
type dashboardData struct {
	Lang          i18n.Lang
	Langs         []i18n.Lang
	Path          string
	User          *database.User
	APIKey        *database.APIKey
	NewKey        string
	IsAdmin       bool
	APIBaseURL    string
	ExtendUntil   string
	ExpiresInDays int
	ExpiryUrgent  bool
	ProfileName   string
	QuotaTokens   string
	QuotaPeriod   string
	Models        []string
	Usage         usageReport
	CSRFToken     string
}

// audit records a self-service action, attributing it to the user themselves.
// A failed audit write must not fail the user's action.
func (u *UI) audit(r *http.Request, action string, user *database.User, detail string) {
	if err := u.store.RecordAudit(r.Context(), &database.AuditEvent{
		Action:       action,
		ActorEmail:   user.Email,
		SubjectEmail: user.Email,
		SubjectID:    &user.ID,
		Detail:       detail,
	}); err != nil {
		slog.Error("record audit event", "action", action, "err", err)
	}
}

// Dashboard renders the user dashboard.
func (u *UI) Dashboard(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	apiKey, err := u.store.GetAPIKeyByUser(r.Context(), su.User.ID)
	if err != nil {
		slog.Error("dashboard: load key", "err", err)
	}

	lang := i18n.FromRequest(r)

	// The dashboard advertises the extend duration and the fair-use quota, both
	// of which come from the user's profile rather than the server default.
	profile, err := u.resolveProfile(r, su.User)
	if err != nil {
		slog.Error("dashboard: resolve profile", "err", err)
	}

	// Redeem a one-time new key stashed by GenerateKey. The secret never
	// appears in the URL; the query string carries only an opaque token.
	newKey := u.flash.Take(su.User.ID, r.URL.Query().Get("k"))

	if err := u.tmpl.Execute(w, dashboardData{
		User:          su.User,
		APIKey:        apiKey,
		NewKey:        newKey,
		IsAdmin:       u.cfg.IsAdmin(su.User.Email),
		APIBaseURL:    strings.TrimRight(u.cfg.LiteLLMBaseURL, "/") + "/v1",
		ExtendUntil:   u.extendUntil(profile),
		ExpiresInDays: daysUntilExpiry(apiKey),
		ExpiryUrgent:  isExpiryUrgent(apiKey),
		ProfileName:   profileName(profile),
		QuotaTokens:   profileQuota(profile),
		QuotaPeriod:   profilePeriod(profile),
		Models:        u.userModels(r.Context(), profile),
		Usage:         u.userUsage(r.Context(), apiKey),
		CSRFToken:     u.csrf.Token(w, r),
		Lang:          lang,
		Langs:         i18n.Supported,
		Path:          r.URL.Path,
	}); err != nil {
		slog.Error("dashboard template", "err", err)
	}
}

// Login redirects to the OIDC provider.
func (u *UI) Login(w http.ResponseWriter, r *http.Request) {
	u.oidc.LoginHandler(w, r)
}

// Callback handles the OIDC authorization code callback.
func (u *UI) Callback(w http.ResponseWriter, r *http.Request) {
	result, err := u.oidc.HandleCallback(w, r)
	if err != nil {
		slog.Error("callback: OIDC error", "err", err)
		http.Error(w, "Authentication failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	user, err := u.oidc.GetOrCreateUser(r.Context(), result.UserInfo)
	if err != nil {
		slog.Error("callback: get/create user", "err", err)
		http.Error(w, "Failed to load user", http.StatusInternalServerError)
		return
	}

	token, err := u.sessions.Create(r.Context(), user.ID, result.IDToken)
	if err != nil {
		slog.Error("callback: create session", "user_id", user.ID, "err", err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	u.sessions.SetCookie(w, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

// Logout clears the session and optionally redirects to the OIDC logout endpoint.
func (u *UI) Logout(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	logoutURL := ""
	if err == nil {
		logoutURL = u.oidc.LogoutURL(su.IDToken)
		_ = u.sessions.Delete(r.Context(), su.SessionID)
	}
	u.sessions.ClearCookie(w)
	if logoutURL != "" {
		http.Redirect(w, r, logoutURL, http.StatusFound)
	} else {
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// BackchannelLogout handles OIDC back-channel logout requests.
func (u *UI) BackchannelLogout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	rawToken := r.FormValue("logout_token")
	sub, err := u.oidc.ValidateLogoutToken(r.Context(), rawToken)
	if err != nil {
		http.Error(w, "invalid logout token", http.StatusBadRequest)
		return
	}
	if err := u.sessions.DeleteByOIDCSub(r.Context(), sub); err != nil {
		slog.Error("backchannel logout: delete sessions", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

// SessionStatus returns 200 if the session is valid, 401 otherwise.
func (u *UI) SessionStatus(w http.ResponseWriter, r *http.Request) {
	_, err := u.requireSession(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// keyAlias builds the label a key carries in the provider's UI. LiteLLM requires
// aliases to be unique across all live keys, and rotation deliberately creates
// the replacement while the old key is still live, so the address alone would
// collide on every regeneration.
//
// The suffix is random rather than a timestamp: two rotations can land in the
// same millisecond, so a clock-derived suffix is not actually unique. The
// address keeps the alias readable; ownership is tracked by KeyRequest.Owner.
func keyAlias(email string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Falling back to a bare address would 400 on the next rotation, so
		// fail here rather than issue a key that cannot be replaced.
		return "", fmt.Errorf("generate key alias: %w", err)
	}
	return email + "-" + hex.EncodeToString(b), nil
}

// GenerateKey creates a new LiteLLM key for the user, replacing any existing one.
func (u *UI) GenerateKey(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	existing, err := u.store.GetAPIKeyByUser(r.Context(), su.User.ID)
	if err != nil {
		slog.Error("look up existing key", "err", err)
		http.Error(w, "Failed to load your key", http.StatusInternalServerError)
		return
	}

	profile, err := u.resolveProfile(r, su.User)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}

	alias, err := keyAlias(su.User.Email)
	if err != nil {
		slog.Error("build key alias", "err", err)
		metrics.KeyOperations.WithLabelValues("generate", "alias_error").Inc()
		http.Error(w, "Failed to create key", http.StatusInternalServerError)
		return
	}

	// Create the replacement BEFORE revoking the old one. The reverse order
	// leaves the user with no working key whenever creation fails.
	expiresAt := time.Now().AddDate(0, 0, u.keyDuration(profile))
	result, err := u.keys.CreateKey(r.Context(), keyprovider.KeyRequest{
		Alias:     alias,
		Owner:     su.User.Email,
		ExpiresAt: expiresAt,
		Limits:    profileLimits(profile),
	})
	if err != nil {
		metrics.KeyOperations.WithLabelValues("generate", "provider_error").Inc()
		http.Error(w, "Failed to create key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	key := result.Secret
	prefix := key
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	if err := u.store.ReplaceAPIKey(r.Context(), &database.APIKey{
		UserID:     su.User.ID,
		LiteLLMKey: result.Ref,
		KeyPrefix:  prefix,
		ExpiresAt:  expiresAt,
	}); err != nil {
		// The key exists upstream but could not be recorded, so nothing can
		// ever revoke it through this app. Revoke it now rather than leaking it.
		slog.Error("store api key", "err", err)
		if delErr := u.keys.DeleteKey(r.Context(), result.Ref); delErr != nil {
			slog.Error("roll back orphaned key", "key_prefix", prefix, "err", delErr)
		}
		metrics.KeyOperations.WithLabelValues("generate", "store_error").Inc()
		http.Error(w, "Failed to save your key", http.StatusInternalServerError)
		return
	}

	// The new key is stored; the old one is now unreferenced and safe to
	// revoke. A failure here leaves a stale key that expires on its own.
	if existing != nil {
		if delErr := u.keys.DeleteKey(r.Context(), existing.LiteLLMKey); delErr != nil {
			slog.Error("revoke replaced key", "key_prefix", existing.KeyPrefix, "err", delErr)
		}
	}

	metrics.KeyOperations.WithLabelValues("generate", "success").Inc()
	u.audit(r, database.AuditKeyGenerated, su.User, "key "+prefix)

	token, err := u.flash.Put(su.User.ID, key)
	if err != nil {
		slog.Error("stash new key", "err", err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/?k="+token, http.StatusFound)
}

// ExtendKey extends the user's key expiry by KEY_DURATION_DAYS from now.
func (u *UI) ExtendKey(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	k, err := u.store.GetAPIKeyByUser(r.Context(), su.User.ID)
	if err != nil {
		http.Error(w, "Failed to load your key", http.StatusInternalServerError)
		return
	}
	if k == nil {
		http.Error(w, "No key found", http.StatusBadRequest)
		return
	}

	profile, err := u.resolveProfile(r, su.User)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}
	newExpiry := time.Now().AddDate(0, 0, u.keyDuration(profile))
	if err := u.keys.UpdateExpiry(r.Context(), k.LiteLLMKey, newExpiry); err != nil {
		http.Error(w, "Failed to extend key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := u.store.UpdateAPIKeyExpiry(r.Context(), k.ID, newExpiry); err != nil {
		slog.Error("update key expiry in db", "err", err)
	}

	metrics.KeyOperations.WithLabelValues("extend", "success").Inc()
	u.audit(r, database.AuditKeyExtended, su.User, "until "+newExpiry.Format("2006-01-02"))

	http.Redirect(w, r, "/", http.StatusFound)
}

// DeleteKey removes the user's LiteLLM key.
func (u *UI) DeleteKey(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	k, err := u.store.GetAPIKeyByUser(r.Context(), su.User.ID)
	if err != nil || k == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if err := u.keys.DeleteKey(r.Context(), k.LiteLLMKey); err != nil {
		slog.Error("delete LiteLLM key", "err", err)
	}
	_ = u.store.DeleteAPIKey(r.Context(), k.ID)
	metrics.KeyOperations.WithLabelValues("delete", "success").Inc()
	u.audit(r, database.AuditKeyDeleted, su.User, "key "+k.KeyPrefix)
	http.Redirect(w, r, "/", http.StatusFound)
}

// daysUntilExpiry reports whole days remaining; negative once expired.
func daysUntilExpiry(k *database.APIKey) int {
	if k == nil {
		return 0
	}
	return int(time.Until(k.ExpiresAt).Hours() / 24)
}

// isExpiryUrgent marks the point where the dashboard nags rather than informs.
// It matches the widest email threshold so both channels agree.
func isExpiryUrgent(k *database.APIKey) bool {
	if k == nil {
		return false
	}
	return time.Until(k.ExpiresAt) < 14*24*time.Hour
}

func profileName(p *database.Profile) string {
	if p == nil {
		return ""
	}
	return p.Name
}

// profileQuota renders the fair-use allowance, or "" when there is none.
func profileQuota(p *database.Profile) string {
	if p == nil || p.QuotaTokens <= 0 || p.QuotaPeriod == "" {
		return ""
	}
	return litellm.FormatTokens(p.QuotaTokens)
}

func profilePeriod(p *database.Profile) string {
	if p == nil || p.QuotaTokens <= 0 {
		return ""
	}
	switch p.QuotaPeriod {
	case "1h":
		return "per hour"
	case "24h":
		return "per day"
	case "7d":
		return "per week"
	case "30d":
		return "per month"
	default:
		return ""
	}
}

// keyDuration returns the profile's expiry in days, falling back to the
// server-wide default when the profile does not set one.
func (u *UI) keyDuration(p *database.Profile) int {
	if p != nil && p.KeyDurationDays > 0 {
		return p.KeyDurationDays
	}
	return u.cfg.KeyDurationDays
}

// usageReport is what the dashboard shows about consumption.
type usageReport struct {
	Days  []keyprovider.DailyUsage
	Total int64
	// Peak is the busiest day's total, used to scale the bars. Zero when there
	// is no traffic, and callers must not divide by it unchecked.
	Peak int64
}

// userUsage summarises what the user's current key has consumed. Usage belongs
// to a key rather than a person upstream, so regenerating starts the history
// over — the new key is a different key.
//
// An empty report means "show nothing": no key, a gateway that cannot be
// reached, or a provider that does not report usage at all.
func (u *UI) userUsage(ctx context.Context, k *database.APIKey) usageReport {
	if k == nil {
		return usageReport{}
	}
	days := u.usage.Days(ctx, k.LiteLLMKey)

	var rep usageReport
	rep.Days = days
	for _, d := range days {
		rep.Total += d.Tokens
		if d.Tokens > rep.Peak {
			rep.Peak = d.Tokens
		}
	}
	return rep
}

// userModels is the model list to show a user: the ones their key will
// actually accept. A profile that restricts models is authoritative — listing
// everything the gateway serves would advertise models whose requests the key
// is rejected for. An unrestricted profile sees the whole gateway list.
//
// Returns nothing when the gateway cannot be reached or cannot enumerate, so
// the dashboard omits the row rather than showing an empty one.
func (u *UI) userModels(ctx context.Context, p *database.Profile) []string {
	if p != nil && len(p.Models) > 0 {
		return p.Models
	}
	return u.models.Models(ctx)
}

// extendUntil is the expiry date the Extend button will set, formatted for
// display. Extend moves expiry to a full period from now rather than adding to
// whatever remains, so the button names the resulting date instead of promising
// "+N days" — an addition it does not perform. Same source as ExtendKey, so the
// two cannot drift.
func (u *UI) extendUntil(p *database.Profile) string {
	return time.Now().AddDate(0, 0, u.keyDuration(p)).Format("2006-01-02")
}

func (u *UI) resolveProfile(r *http.Request, user *database.User) (*database.Profile, error) {
	if user.ProfileID != nil {
		return u.store.GetProfile(r.Context(), *user.ProfileID)
	}
	return u.store.GetDefaultProfile(r.Context())
}

// profileLimits maps a profile onto the provider-neutral limits. Translating
// those into a specific gateway's wire format is the adapter's job.
func profileLimits(p *database.Profile) keyprovider.Limits {
	if p == nil {
		return keyprovider.Limits{}
	}
	return keyprovider.Limits{
		Models:            p.Models,
		TokensPerMinute:   p.TPMLimit,
		RequestsPerMinute: p.RPMLimit,
		QuotaTokens:       p.QuotaTokens,
		QuotaPeriod:       p.QuotaPeriod,
	}
}
