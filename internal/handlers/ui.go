package handlers

import (
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/litellm"
	oidcpkg "github.com/virtuos/ai-self-service/internal/oidc"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

type UI struct {
	cfg      *config.Config
	store    *database.Store
	sessions *session.Manager
	oidc     *oidcpkg.Provider
	litellm  *litellm.Client
	tmpl     *template.Template
	flash    *keyFlash
	csrf     *session.CSRF
}

func NewUI(cfg *config.Config, store *database.Store, sessions *session.Manager, oidc *oidcpkg.Provider, ll *litellm.Client, csrf *session.CSRF) *UI {
	tmpl := template.Must(template.ParseFS(web.TemplateFS, "templates/dashboard.html"))
	return &UI{cfg: cfg, store: store, sessions: sessions, oidc: oidc, litellm: ll, tmpl: tmpl, flash: newKeyFlash(), csrf: csrf}
}

func (u *UI) requireSession(r *http.Request) (*session.SessionUser, error) {
	token := u.sessions.TokenFromRequest(r)
	if token == "" {
		return nil, http.ErrNoCookie
	}
	return u.sessions.Get(r.Context(), token)
}

// dashboardData is what dashboard.html renders. Named rather than anonymous so
// tests cannot drift from the handler's shape.
type dashboardData struct {
	User            *database.User
	APIKey          *database.APIKey
	NewKey          string
	IsAdmin         bool
	FrontendURL     string
	KeyDurationDays int
	ProfileName     string
	QuotaTokens     string
	QuotaPeriod     string
	CSRFToken       string
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
		log.Printf("dashboard: load key: %v", err)
	}

	// The dashboard advertises the extend duration and the fair-use quota, both
	// of which come from the user's profile rather than the server default.
	profile, err := u.resolveProfile(r, su.User)
	if err != nil {
		log.Printf("dashboard: resolve profile: %v", err)
	}

	// Redeem a one-time new key stashed by GenerateKey. The secret never
	// appears in the URL; the query string carries only an opaque token.
	newKey := u.flash.Take(su.User.ID, r.URL.Query().Get("k"))

	if err := u.tmpl.Execute(w, dashboardData{
		User:            su.User,
		APIKey:          apiKey,
		NewKey:          newKey,
		IsAdmin:         u.cfg.IsAdmin(su.User.Email),
		FrontendURL:     u.cfg.FrontendURL,
		KeyDurationDays: u.keyDuration(profile),
		ProfileName:     profileName(profile),
		QuotaTokens:     profileQuota(profile),
		QuotaPeriod:     profilePeriod(profile),
		CSRFToken:       u.csrf.Token(w, r),
	}); err != nil {
		log.Printf("dashboard template: %v", err)
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
		log.Printf("callback: OIDC error: %v", err)
		http.Error(w, "Authentication failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	user, err := u.oidc.GetOrCreateUser(r.Context(), result.UserInfo)
	if err != nil {
		log.Printf("callback: get/create user: %v", err)
		http.Error(w, "Failed to load user", http.StatusInternalServerError)
		return
	}

	token, err := u.sessions.Create(r.Context(), user.ID, result.IDToken)
	if err != nil {
		log.Printf("callback: create session for user %d: %v", user.ID, err)
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
		log.Printf("backchannel logout: delete sessions: %v", err)
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

// GenerateKey creates a new LiteLLM key for the user, replacing any existing one.
func (u *UI) GenerateKey(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	existing, err := u.store.GetAPIKeyByUser(r.Context(), su.User.ID)
	if err != nil {
		log.Printf("look up existing key: %v", err)
		http.Error(w, "Failed to load your key", http.StatusInternalServerError)
		return
	}

	profile, err := u.resolveProfile(r, su.User)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}

	// Create the replacement BEFORE revoking the old one. The reverse order
	// leaves the user with no working key whenever creation fails.
	expiresAt := time.Now().AddDate(0, 0, u.keyDuration(profile))
	params := profileToKeyParams(profile, su.User.Email)
	key, err := u.litellm.CreateKey(r.Context(), su.User.Email, params, expiresAt)
	if err != nil {
		http.Error(w, "Failed to create key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	prefix := key
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	if err := u.store.ReplaceAPIKey(r.Context(), &database.APIKey{
		UserID:     su.User.ID,
		LiteLLMKey: key,
		KeyPrefix:  prefix,
		ExpiresAt:  expiresAt,
	}); err != nil {
		// The key exists upstream but could not be recorded, so nothing can
		// ever revoke it through this app. Revoke it now rather than leaking it.
		log.Printf("store api key: %v", err)
		if delErr := u.litellm.DeleteKey(r.Context(), key); delErr != nil {
			log.Printf("roll back orphaned LiteLLM key %s: %v", prefix, delErr)
		}
		http.Error(w, "Failed to save your key", http.StatusInternalServerError)
		return
	}

	// The new key is stored; the old one is now unreferenced and safe to
	// revoke. A failure here leaves a stale key that expires on its own.
	if existing != nil {
		if delErr := u.litellm.DeleteKey(r.Context(), existing.LiteLLMKey); delErr != nil {
			log.Printf("revoke replaced LiteLLM key %s: %v", existing.KeyPrefix, delErr)
		}
	}

	token, err := u.flash.Put(su.User.ID, key)
	if err != nil {
		log.Printf("stash new key: %v", err)
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
	if err := u.litellm.UpdateKeyExpiry(r.Context(), k.LiteLLMKey, newExpiry); err != nil {
		http.Error(w, "Failed to extend key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := u.store.UpdateAPIKeyExpiry(r.Context(), k.ID, newExpiry); err != nil {
		log.Printf("update key expiry in db: %v", err)
	}

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

	if err := u.litellm.DeleteKey(r.Context(), k.LiteLLMKey); err != nil {
		log.Printf("delete LiteLLM key: %v", err)
	}
	_ = u.store.DeleteAPIKey(r.Context(), k.ID)
	http.Redirect(w, r, "/", http.StatusFound)
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

func (u *UI) resolveProfile(r *http.Request, user *database.User) (*database.Profile, error) {
	if user.ProfileID != nil {
		return u.store.GetProfile(r.Context(), *user.ProfileID)
	}
	return u.store.GetDefaultProfile(r.Context())
}

func profileToKeyParams(p *database.Profile, email string) litellm.KeyParams {
	models := p.Models
	if len(models) == 0 {
		models = nil // nil = all models in LiteLLM
	}
	params := litellm.KeyParams{
		Models:         models,
		TPMLimit:       p.TPMLimit,
		RPMLimit:       p.RPMLimit,
		MaxBudget:      p.MaxBudget,
		BudgetDuration: p.BudgetDuration,
		Metadata:       map[string]any{"user_email": email},
	}

	// A token quota is expressed upstream as a spend cap over a reset window.
	// It takes precedence over any raw budget left on the profile, which only
	// applies once commercial (really priced) models are in play.
	if p.QuotaTokens > 0 && p.QuotaPeriod != "" {
		budget := litellm.TokensToBudget(p.QuotaTokens)
		period := p.QuotaPeriod
		params.MaxBudget = &budget
		params.BudgetDuration = &period
	}
	return params
}
