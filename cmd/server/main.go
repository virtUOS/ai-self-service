package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/handlers"
	"github.com/virtuos/ai-self-service/internal/litellm"
	oidcpkg "github.com/virtuos/ai-self-service/internal/oidc"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// ── Database ──────────────────────────────────────────────────────────────
	// SQLite only. The dataset is a few thousand rows at most (one key per
	// user), so a separate database server would add operational cost without
	// buying anything.
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+cfg.DBPath+"?cache=shared&_foreign_keys=on")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	bunDB := bun.NewDB(sqldb, sqlitedialect.New())
	defer bunDB.Close()

	store := database.NewStore(bunDB)
	ctx := context.Background()

	if err := store.RunMigrations(ctx); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	if err := store.SeedDefaultProfile(ctx); err != nil {
		log.Fatalf("seed default profile: %v", err)
	}
	if err := store.DeleteExpiredSessions(ctx); err != nil {
		log.Printf("cleanup sessions: %v", err)
	}

	// ── Dependencies ──────────────────────────────────────────────────────────
	oidcProvider, err := oidcpkg.NewProvider(ctx, cfg, store)
	if err != nil {
		log.Fatalf("OIDC provider: %v", err)
	}

	sessions := session.NewManager(store, cfg.SessionDuration, cfg.CookieSecure)
	csrf, err := session.NewCSRF(cfg.CookieSecure)
	if err != nil {
		log.Fatalf("CSRF: %v", err)
	}
	// The adapter is what the handlers see; swapping gateways means writing a
	// different keyprovider.Provider, not touching the handlers.
	keys := litellm.NewProvider(litellm.NewClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey))

	ui := handlers.NewUI(cfg, store, sessions, oidcProvider, keys, csrf)
	admin := handlers.NewAdmin(cfg, store, sessions, keys, csrf)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(handlers.SecurityHeaders(cfg.CookieSecure))

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS))))

	// Called server-to-server by the OIDC provider, authenticated by the signed
	// logout_token in the body — no browser cookie, so CSRF does not apply.
	r.Post("/backchannel-logout", ui.BackchannelLogout)

	r.Group(func(r chi.Router) {
		r.Use(csrf.Protect)

		r.Get("/login", ui.Login)
		r.Get("/callback", ui.Callback)
		r.Post("/logout", ui.Logout)
		r.Get("/session/status", ui.SessionStatus)

		r.Get("/", ui.Dashboard)
		r.Post("/key/generate", ui.GenerateKey)
		r.Post("/key/extend", ui.ExtendKey)
		r.Post("/key/delete", ui.DeleteKey)
	})

	r.Route("/admin", func(r chi.Router) {
		r.Use(csrf.Protect)
		r.Use(admin.Middleware)
		r.Get("/", admin.Panel)
		r.Post("/profiles", admin.CreateProfile)
		r.Post("/profiles/{id}", admin.UpdateProfile)
		r.Post("/profiles/{id}/delete", admin.DeleteProfile)
		r.Post("/users/{id}/profile", admin.SetUserProfile)
		r.Post("/users/{id}/key/revoke", admin.RevokeUserKey)
	})

	// Liveness/readiness for the reverse proxy and orchestrator.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := bunDB.PingContext(req.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: r,
		// Without a header timeout a stalled client can hold a connection open
		// indefinitely (Slowloris).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Expire stale sessions periodically; previously this ran once at startup
	// and rows accumulated for the lifetime of the process.
	stopCleanup := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := store.DeleteExpiredSessions(context.Background()); err != nil {
					log.Printf("cleanup sessions: %v", err)
				}
			case <-stopCleanup:
				return
			}
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Drain in-flight requests on SIGTERM instead of dropping them.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serverErr:
		log.Fatalf("server: %v", err)
	case <-quit:
		log.Println("shutting down")
	}

	close(stopCleanup)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}
