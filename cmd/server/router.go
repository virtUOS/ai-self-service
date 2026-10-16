package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/handlers"
	"github.com/virtuos/ai-self-service/internal/metrics"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

// pinger is what /readyz needs from the database.
type pinger interface {
	PingContext(ctx context.Context) error
}

// newRouter puts every route behind the middleware it needs. It is separate
// from main so a test can check that, route by route, on the real table.
func newRouter(cfg *config.Config, ui *handlers.UI, admin *handlers.Admin, csrf *session.CSRF, db pinger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(handlers.ClientIP(cfg.TrustedProxies))
	// Before the Logger, so access-log lines carry the ID that error pages show.
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(handlers.MaxBody(handlers.MaxRequestBody))
	// The sign-out form redirects to the OIDC provider, so its origin must be a
	// permitted form-action target.
	idpOrigin := ""
	if u, err := url.Parse(cfg.OIDCIssuerURL); err == nil && u.Scheme != "" {
		idpOrigin = u.Scheme + "://" + u.Host
	}
	r.Use(handlers.SecurityHeaders(cfg.CookieSecure, idpOrigin))
	// chi fills in the matched pattern, so metrics label by route template
	// rather than concrete path — otherwise every user id is a new series.
	r.Use(metrics.Middleware(func(req *http.Request) string {
		if rctx := chi.RouteContext(req.Context()); rctx != nil {
			return rctx.RoutePattern()
		}
		return ""
	}))

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS))))

	// Called server-to-server by the OIDC provider, authenticated by the signed
	// logout_token in the body — no browser cookie, so CSRF does not apply.
	r.Post("/backchannel-logout", ui.BackchannelLogout)

	r.Group(func(r chi.Router) {
		r.Use(handlers.NoStore)
		r.Use(csrf.Protect)

		r.Get("/login", ui.Login)
		r.Get("/callback", ui.Callback)
		r.Post("/logout", ui.Logout)
		r.Get("/session/status", ui.SessionStatus)
		r.Post("/lang", handlers.SetLanguage(cfg.CookieSecure))
		r.Get("/privacy", ui.Privacy)

		r.Get("/", ui.Dashboard)
		r.Post("/key/generate", ui.GenerateKey)
		r.Post("/key/extend", ui.ExtendKey)
		r.Post("/key/delete", ui.DeleteKey)
	})

	r.Route("/admin", func(r chi.Router) {
		r.Use(handlers.NoStore)
		r.Use(csrf.Protect)
		r.Use(admin.Middleware)
		r.Get("/", admin.Panel)
		r.Post("/profiles", admin.CreateProfile)
		r.Post("/profiles/{id}", admin.UpdateProfile)
		r.Post("/profiles/{id}/delete", admin.DeleteProfile)
		r.Post("/users/{id}/profile", admin.SetUserProfile)
		r.Post("/users/{id}/key/revoke", admin.RevokeUserKey)
		r.Post("/admins", admin.GrantAdmin)
		r.Post("/admins/revoke", admin.RevokeAdmin)
	})

	// Scraped by the monitoring host; Caddy restricts it to those IPs.
	r.Handle("/metrics", metrics.Handler())

	// Liveness/readiness for the reverse proxy and orchestrator.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := db.PingContext(req.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return r
}
