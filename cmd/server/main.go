package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
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
	"github.com/virtuos/ai-self-service/internal/metrics"
	"github.com/virtuos/ai-self-service/internal/notify"
	oidcpkg "github.com/virtuos/ai-self-service/internal/oidc"
	"github.com/virtuos/ai-self-service/internal/session"
	"github.com/virtuos/ai-self-service/web"
)

func main() {
	_ = godotenv.Load()

	// Structured logs so the aggregator can filter on fields rather than
	// grepping formatted strings. LOG_LEVEL raises or lowers verbosity without
	// a rebuild; text stays readable in a terminal and in journald.
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil && os.Getenv("LOG_LEVEL") != "" {
		fmt.Fprintf(os.Stderr, "invalid LOG_LEVEL %q, using info\n", os.Getenv("LOG_LEVEL"))
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

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
		slog.Error("cleanup sessions", "err", err)
	}

	// ── Dependencies ──────────────────────────────────────────────────────────
	oidcProvider, err := oidcpkg.NewProvider(ctx, cfg, store)
	if err != nil {
		log.Fatalf("OIDC provider: %v", err)
	}

	sessions := session.NewManager(store, cfg.SessionDuration, cfg.CookieSecure)
	// Seeded from the OIDC client secret: a stable per-deployment secret the
	// process already holds, so CSRF tokens survive a restart instead of
	// invalidating every open page on each redeploy.
	csrf, err := session.NewCSRF(cfg.CookieSecure, cfg.OIDCClientSecret)
	if err != nil {
		log.Fatalf("CSRF: %v", err)
	}
	// The adapter is what the handlers see; swapping gateways means writing a
	// different keyprovider.Provider, not touching the handlers.
	gateway := litellm.NewClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey)
	keys := litellm.NewProvider(gateway)

	// Token quotas are converted to spend caps at the price the gateway
	// charges. Read it now rather than trusting a constant to have been kept
	// in step, and say so when models disagree: LiteLLM enforces one cap per
	// key whatever model a request names, so a dearer model draws the
	// allowance down faster and the token figure stops being exact.
	//
	// A failure here is not fatal. The conversion falls back to the nominal
	// rate, which is what the deployment is expected to use, and the portal is
	// more useful with an approximate quota than not starting at all.
	if err := gateway.RefreshPricing(ctx); err != nil {
		slog.Warn("could not read model pricing; quotas use the nominal rate", "err", err)
	} else if p := gateway.CurrentPricing(); !p.Uniform {
		for _, o := range p.Outliers {
			slog.Warn("model priced differently from the rest; token quotas are approximate",
				"model", o.Model, "input_cost_per_token", o.Input, "output_cost_per_token", o.Output)
		}
		slog.Warn("converting token quotas at the dearest rate so caps are not overshot",
			"token_price", p.TokenPrice)
	}

	ui := handlers.NewUI(cfg, store, sessions, oidcProvider, keys, csrf)
	admin := handlers.NewAdmin(cfg, store, sessions, keys, csrf)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
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
		r.Use(csrf.Protect)

		r.Get("/login", ui.Login)
		r.Get("/callback", ui.Callback)
		r.Post("/logout", ui.Logout)
		r.Get("/session/status", ui.SessionStatus)
		r.Post("/lang", handlers.SetLanguage(cfg.CookieSecure))

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

	// Scraped by the monitoring host; Caddy restricts it to those IPs.
	r.Handle("/metrics", metrics.Handler())

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

	// Warn users before their keys expire. Without SMTP configured this logs
	// what it would have sent; the dashboard warning still reaches anyone who
	// visits.
	var notifier notify.Notifier = notify.Discard{}
	if cfg.SMTPHost != "" {
		notifier = &notify.SMTP{
			Host: cfg.SMTPHost, From: cfg.SMTPFrom,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		}
		slog.Info("expiry notifications enabled", "relay", cfg.SMTPHost)
	} else {
		slog.Warn("SMTP_HOST unset: expiry notifications will not be delivered")
	}
	reminderCtx, stopReminder := context.WithCancel(context.Background())
	go notify.NewReminder(store, notifier, cfg.FrontendURL, nil).
		Start(reminderCtx, 6*time.Hour)

	// Refresh key gauges alongside the other periodic work. Reading them from
	// the database keeps them correct across restarts.
	refreshGauges := func() {
		ctx := context.Background()
		keys, err := store.ListAPIKeys(ctx)
		if err != nil {
			slog.Error("metrics: list keys", "err", err)
			return
		}
		soon := 0
		cutoff := time.Now().AddDate(0, 0, 7)
		for _, k := range keys {
			if k.ExpiresAt.Before(cutoff) {
				soon++
			}
		}
		metrics.SetKeyGauges(len(keys), soon)
	}
	refreshGauges()

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
					slog.Error("cleanup sessions", "err", err)
				}
				refreshGauges()
			case <-stopCleanup:
				return
			}
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr)
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
		slog.Info("shutting down")
	}

	close(stopCleanup)
	stopReminder()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown", "err", err)
	}
}
