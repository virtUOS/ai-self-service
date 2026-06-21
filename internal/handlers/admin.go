package handlers

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

type Admin struct {
	cfg      *config.Config
	store    *database.Store
	sessions *session.Manager
	tmpl     *template.Template
}

func NewAdmin(cfg *config.Config, store *database.Store, sessions *session.Manager) *Admin {
	tmpl := template.Must(template.ParseFS(web.TemplateFS, "templates/admin.html"))
	return &Admin{cfg: cfg, store: store, sessions: sessions, tmpl: tmpl}
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
}

type adminData struct {
	Profiles []database.Profile
	Users    []userRow
	Flash    string
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
		log.Printf("list users: %v", err)
		http.Error(w, "Failed to load users", http.StatusInternalServerError)
		return
	}
	users := make([]userRow, len(rawUsers))
	for i, u := range rawUsers {
		row := userRow{User: u}
		if u.ProfileID != nil {
			row.ProfileIDVal = *u.ProfileID
		}
		users[i] = row
	}
	flash := r.URL.Query().Get("flash")
	if err := a.tmpl.Execute(w, adminData{Profiles: profiles, Users: users, Flash: flash}); err != nil {
		log.Printf("admin template: %v", err)
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
		Models:      parseModelsField(r.FormValue("models")),
		IsDefault:   r.FormValue("is_default") == "on",
	}
	p.TPMLimit = parseOptionalInt64(r.FormValue("tpm_limit"))
	p.RPMLimit = parseOptionalInt64(r.FormValue("rpm_limit"))
	p.MaxBudget = parseOptionalFloat64(r.FormValue("max_budget"))
	p.BudgetDuration = parseOptionalString(r.FormValue("budget_duration"))

	if p.Name == "" {
		http.Redirect(w, r, "/admin?flash=Name+is+required", http.StatusFound)
		return
	}

	if err := a.store.CreateProfile(r.Context(), p); err != nil {
		log.Printf("create profile: %v", err)
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
		Models:      parseModelsField(r.FormValue("models")),
		IsDefault:   r.FormValue("is_default") == "on",
		UpdatedAt:   time.Now(),
	}
	p.TPMLimit = parseOptionalInt64(r.FormValue("tpm_limit"))
	p.RPMLimit = parseOptionalInt64(r.FormValue("rpm_limit"))
	p.MaxBudget = parseOptionalFloat64(r.FormValue("max_budget"))
	p.BudgetDuration = parseOptionalString(r.FormValue("budget_duration"))

	if err := a.store.UpdateProfile(r.Context(), p); err != nil {
		log.Printf("update profile: %v", err)
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
		log.Printf("delete profile: %v", err)
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
		log.Printf("set user profile: %v", err)
		http.Redirect(w, r, "/admin?flash=Failed+to+update+user+profile", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin?flash=User+profile+updated", http.StatusFound)
}

// --- helpers ---

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

func parseOptionalFloat64(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func parseOptionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
