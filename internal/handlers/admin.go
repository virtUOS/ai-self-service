package handlers

import (
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
	"github.com/virtuos/ai-self-service/internal/litellm"
	"github.com/virtuos/ai-self-service/internal/metrics"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

type Admin struct {
	cfg      *config.Config
	store    *database.Store
	sessions *session.Manager
	keys     keyprovider.Provider
	models   *modelCache
	tmpl     *template.Template
	csrf     *session.CSRF
}

// formatPeriod renders a reset window as words, so the table reads
// "1.5M per day" rather than "1.5M 24h".
func formatPeriod(p string) string {
	switch p {
	case "1h":
		return "per hour"
	case "24h":
		return "per day"
	case "7d":
		return "per week"
	case "30d":
		return "per month"
	default:
		return p
	}
}

// parseAdminTemplate builds the admin template with its helper functions.
// Tests use it too, so a helper added here cannot be missed there.
func parseAdminTemplate() *template.Template {
	funcs := langFuncs()
	funcs["fmtTokens"] = litellm.FormatTokens
	funcs["fmtPeriod"] = formatPeriod
	return template.Must(template.New("admin.html").
		Funcs(funcs).
		ParseFS(web.TemplateFS, "templates/admin.html"))
}

func NewAdmin(cfg *config.Config, store *database.Store, sessions *session.Manager, keys keyprovider.Provider, csrf *session.CSRF) *Admin {
	tmpl := parseAdminTemplate()
	// Only some gateways can enumerate models; the form degrades to free text
	// when the provider cannot.
	lister, _ := keys.(keyprovider.ModelLister)
	return &Admin{cfg: cfg, store: store, sessions: sessions, keys: keys,
		models: newModelCache(lister), tmpl: tmpl, csrf: csrf}
}

// actorEmail identifies the admin performing the current request, for audit.
func (a *Admin) actorEmail(r *http.Request) string {
	su, err := a.sessions.Get(r.Context(), a.sessions.TokenFromRequest(r))
	if err != nil || su == nil {
		return "unknown"
	}
	return su.User.Email
}

// audit records an event without failing the request if the write fails.
func (a *Admin) audit(r *http.Request, action, subjectEmail string, subjectID *int64, detail string) {
	if err := a.store.RecordAudit(r.Context(), &database.AuditEvent{
		Action:       action,
		ActorEmail:   a.actorEmail(r),
		SubjectEmail: subjectEmail,
		SubjectID:    subjectID,
		Detail:       detail,
	}); err != nil {
		slog.Error("record audit event", "action", action, "err", err)
	}
}

// RevokeUserKey handles POST /admin/users/{id}/key/revoke, deleting another
// user's key both upstream and locally.
func (a *Admin) RevokeUserKey(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	user, err := a.store.GetUserByID(r.Context(), userID)
	if err != nil {
		http.Redirect(w, r, "/admin?flash=User+not+found#users", http.StatusFound)
		return
	}

	key, err := a.store.GetAPIKeyByUser(r.Context(), userID)
	if err != nil {
		slog.Error("revoke: load key", "user_id", userID, "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+load+key#users", http.StatusFound)
		return
	}
	if key == nil {
		http.Redirect(w, r, "/admin?flash=User+has+no+key#users", http.StatusFound)
		return
	}

	// Revoke upstream first: if that fails the key is still live, so the local
	// row must stay to keep it revocable.
	if err := a.keys.DeleteKey(r.Context(), key.LiteLLMKey); err != nil {
		slog.Error("revoke: delete upstream key", "key_prefix", key.KeyPrefix, "err", err)
		metrics.KeyOperations.WithLabelValues("revoke", "provider_error").Inc()
		http.Redirect(w, r, "/admin?flash=Failed+to+revoke+key+upstream#users", http.StatusFound)
		return
	}
	if err := a.store.DeleteAPIKey(r.Context(), key.ID); err != nil {
		slog.Error("revoke: delete local key row", "key_id", key.ID, "err", err)
	}

	metrics.KeyOperations.WithLabelValues("revoke", "success").Inc()
	a.audit(r, database.AuditKeyRevoked, user.Email, &userID, "key "+key.KeyPrefix)
	http.Redirect(w, r, "/admin?flash=Key+revoked#users", http.StatusFound)
}

// Middleware checks that the current session belongs to an admin user.
func (a *Admin) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := a.sessions.TokenFromRequest(r)
		if token == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		su, err := a.sessions.Get(r.Context(), token)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if !a.cfg.IsAdmin(su.User.Email) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type userRow struct {
	database.User
	ProfileIDVal int64 // 0 if no profile assigned
	KeyPrefix    string
	KeyExpires   string
	KeyExpired   bool
	HasKey       bool
}

type adminData struct {
	Lang            i18n.Lang
	Langs           []i18n.Lang
	Path            string
	AvailableModels []string
	// DefaultKeyDays is the server-wide expiry a profile falls back to, shown
	// so "default" in the table is a number rather than a mystery.
	DefaultKeyDays int
	Profiles       []database.Profile
	Users          []userRow
	Audit          []database.AuditEvent
	Flash          string
	CSRFToken      string
}

// Panel renders the admin page with profile and user lists.
func (a *Admin) Panel(w http.ResponseWriter, r *http.Request) {
	profiles, err := a.store.ListProfiles(r.Context())
	if err != nil {
		http.Error(w, "Failed to load profiles", http.StatusInternalServerError)
		return
	}
	rawUsers, err := a.store.ListUsers(r.Context())
	if err != nil {
		slog.Error("list users", "err", err)
		http.Error(w, "Failed to load users", http.StatusInternalServerError)
		return
	}
	// One query for all keys rather than one per user.
	keys, err := a.store.ListAPIKeys(r.Context())
	if err != nil {
		slog.Error("list api keys", "err", err)
	}
	keyByUser := make(map[int64]database.APIKey, len(keys))
	for _, k := range keys {
		keyByUser[k.UserID] = k
	}

	users := make([]userRow, len(rawUsers))
	for i, u := range rawUsers {
		row := userRow{User: u}
		if u.ProfileID != nil {
			row.ProfileIDVal = *u.ProfileID
		}
		if k, ok := keyByUser[u.ID]; ok {
			row.HasKey = true
			row.KeyPrefix = k.KeyPrefix
			row.KeyExpires = k.ExpiresAt.Format("2006-01-02")
			row.KeyExpired = time.Now().After(k.ExpiresAt)
		}
		users[i] = row
	}

	audit, err := a.store.ListAuditEvents(r.Context(), 50)
	if err != nil {
		slog.Error("list audit events", "err", err)
	}
	flash := r.URL.Query().Get("flash")
	if err := a.tmpl.Execute(w, adminData{
		Lang:            i18n.FromRequest(r),
		Langs:           i18n.Supported,
		Path:            r.URL.Path,
		AvailableModels: a.models.Models(r.Context()),
		DefaultKeyDays:  a.cfg.KeyDurationDays,
		Profiles:        profiles,
		Users:           users,
		Audit:           audit,
		Flash:           flash,
		CSRFToken:       a.csrf.Token(w, r),
	}); err != nil {
		slog.Error("admin template", "err", err)
	}
}

// CreateProfile handles POST /admin/profiles.
func (a *Admin) CreateProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	p := &database.Profile{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Models:      selectedModels(r),
		IsDefault:   r.FormValue("is_default") == "on",
	}
	p.TPMLimit = parseOptionalInt64(r.FormValue("tpm_limit"))
	p.RPMLimit = parseOptionalInt64(r.FormValue("rpm_limit"))
	p.KeyDurationDays = parseNonNegativeInt(r.FormValue("key_duration_days"))
	p.QuotaTokens = parseNonNegativeInt64(r.FormValue("quota_tokens"))
	p.QuotaPeriod = strings.TrimSpace(r.FormValue("quota_period"))

	if p.Name == "" {
		http.Redirect(w, r, "/admin?flash=Name+is+required", http.StatusFound)
		return
	}
	if !litellm.IsValidQuotaPeriod(p.QuotaPeriod) {
		http.Redirect(w, r, "/admin?flash=Invalid+quota+period", http.StatusFound)
		return
	}

	if err := a.store.CreateProfile(r.Context(), p); err != nil {
		slog.Error("create profile", "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+create+profile", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin?flash=Profile+created", http.StatusFound)
}

// UpdateProfile handles POST /admin/profiles/{id}.
func (a *Admin) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	p := &database.Profile{
		ID:          id,
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Models:      selectedModels(r),
		IsDefault:   r.FormValue("is_default") == "on",
		UpdatedAt:   time.Now(),
	}
	p.TPMLimit = parseOptionalInt64(r.FormValue("tpm_limit"))
	p.RPMLimit = parseOptionalInt64(r.FormValue("rpm_limit"))
	p.KeyDurationDays = parseNonNegativeInt(r.FormValue("key_duration_days"))
	p.QuotaTokens = parseNonNegativeInt64(r.FormValue("quota_tokens"))
	p.QuotaPeriod = strings.TrimSpace(r.FormValue("quota_period"))

	if !litellm.IsValidQuotaPeriod(p.QuotaPeriod) {
		http.Redirect(w, r, "/admin?flash=Invalid+quota+period", http.StatusFound)
		return
	}

	if err := a.store.UpdateProfile(r.Context(), p); err != nil {
		slog.Error("update profile", "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+update+profile", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin?flash=Profile+updated", http.StatusFound)
}

// DeleteProfile handles POST /admin/profiles/{id}/delete.
func (a *Admin) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := a.store.DeleteProfile(r.Context(), id); err != nil {
		slog.Error("delete profile", "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+delete+profile", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin?flash=Profile+deleted", http.StatusFound)
}

// SetUserProfile handles POST /admin/users/{id}/profile.
func (a *Admin) SetUserProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var profileID *int64
	if v := r.FormValue("profile_id"); v != "" && v != "0" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			profileID = &id
		}
	}

	if err := a.store.SetUserProfile(r.Context(), userID, profileID); err != nil {
		slog.Error("set user profile", "err", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+update+user+profile", http.StatusFound)
		return
	}

	detail := "cleared"
	if profileID != nil {
		if p, err := a.store.GetProfile(r.Context(), *profileID); err == nil {
			detail = p.Name
		}
	}
	subjectEmail := ""
	if u, err := a.store.GetUserByID(r.Context(), userID); err == nil {
		subjectEmail = u.Email
	}
	a.audit(r, database.AuditProfileSet, subjectEmail, &userID, detail)

	http.Redirect(w, r, "/admin?flash=User+profile+updated#users", http.StatusFound)
}

// --- helpers ---

// selectedModels reads the model checkboxes, falling back to the comma-
// separated text field when the picker was not rendered. An empty result means
// "all models", which is what the gateway understands from an empty list.
func selectedModels(r *http.Request) []string {
	if vals := r.Form["model"]; len(vals) > 0 {
		out := make([]string, 0, len(vals))
		for _, v := range vals {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return parseModelsField(r.FormValue("models"))
}

func parseModelsField(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseOptionalInt64(s string) *int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

// parseNonNegativeInt reads an optional whole number, treating blank, invalid
// and negative input as 0 ("not set").
func parseNonNegativeInt(s string) int {
	v := parseNonNegativeInt64(s)
	return int(v)
}

func parseNonNegativeInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
