package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/handlers"
	"github.com/virtuos/keyosk/internal/limitsync"
	"github.com/virtuos/keyosk/internal/litellm"
	"github.com/virtuos/keyosk/internal/metrics"
	"github.com/virtuos/keyosk/internal/notify"
	oidcpkg "github.com/virtuos/keyosk/internal/oidc"
	"github.com/virtuos/keyosk/internal/profileexpiry"
	"github.com/virtuos/keyosk/internal/session"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check /healthz of the running server and exit")
	resync := flag.Bool("resync-limits", false, "mark every key for a limit resync by the running server and exit")
	flag.Parse()

	_ = godotenv.Load()

	// Container health check: the image has no shell or wget, so the binary
	// probes itself. Runs before config.Load so it needs none of the required
	// settings, only LISTEN_ADDR.
	if *healthcheck {
		url, err := healthcheckURL(config.ListenAddr())
		if err == nil {
			err = checkHealth(url)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "unhealthy: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Undo changes made directly in the gateway: the running server pushes
	// every key's limits again. Needs only DB_PATH, like -healthcheck needs
	// only LISTEN_ADDR, so a cron job can run it with a minimal environment.
	if *resync {
		if err := resyncLimits(context.Background(), config.DBPath(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "resync failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

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
		fatal("config", err)
	}

	// ── Database ──────────────────────────────────────────────────────────────
	// SQLite only. The dataset is a few thousand rows at most (one key per
	// user), so a separate database server would add operational cost without
	// buying anything.
	bunDB, err := database.Open(cfg.DBPath)
	if err != nil {
		fatal("database", err)
	}
	defer bunDB.Close()

	store := database.NewStore(bunDB)
	ctx := context.Background()

	if err := store.RunMigrations(ctx); err != nil {
		fatal("migrations", err)
	}
	if err := store.SeedDefaultProfile(ctx); err != nil {
		fatal("seed default profile", err)
	}
	if err := store.DeleteExpiredSessions(ctx); err != nil {
		slog.Error("cleanup sessions", "err", err)
	}

	// Admin rights are recorded on the account (issue #107). An operator who
	// still sets the old settings, or has not marked a first admin yet, would
	// otherwise find nobody can reach the panel and not know why.
	for _, name := range cfg.ObsoleteSettings {
		slog.Warn("setting is no longer read: admin rights are recorded on the account, see README, Admin panel",
			"setting", name)
	}
	if cfg.AdminFromGroups() {
		// Nobody holds the flag until a member of the group logs in, so a
		// count would only raise a false alarm here.
		slog.Info("admin rights come from the login system",
			"group", cfg.AdminGroup, "claim", cfg.AdminGroupClaim)
	} else if n, err := store.CountAdmins(ctx); err != nil {
		slog.Error("count admins", "err", err)
	} else if n == 0 {
		slog.Warn("no account holds the admin panel: mark one with the command in README, Admin panel")
	}

	// ── Dependencies ──────────────────────────────────────────────────────────
	oidcProvider, err := oidcpkg.NewProvider(ctx, cfg, store)
	if err != nil {
		fatal("OIDC provider", err)
	}

	sessions := session.NewManager(store, cfg.SessionDuration, cfg.CookieSecure)
	// Seeded from the OIDC client secret: a stable per-deployment secret the
	// process already holds, so CSRF tokens survive a restart instead of
	// invalidating every open page on each redeploy.
	csrf, err := session.NewCSRF(cfg.CookieSecure, cfg.OIDCClientSecret)
	if err != nil {
		fatal("CSRF", err)
	}
	// The adapter is what the handlers see; swapping gateways means writing a
	// different keyprovider.Provider, not touching the handlers.
	gateway := litellm.NewClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey)
	keys := litellm.NewProvider(gateway)

	// A model priced at zero accrues no spend, so a budget over it never
	// binds. Say so at startup; nothing else about prices needs checking now
	// that quotas are budgets and differing prices are the expected state.
	if p, err := gateway.Pricing(ctx); err != nil {
		slog.Warn("could not read model pricing", "err", err)
	} else {
		for _, m := range p.Unpriced {
			slog.Warn("model is unpriced; quotas do not bind on it", "model", m)
		}
	}

	// Pushes profile limits to keys that are out of date, in the background:
	// one admin change can affect thousands of keys.
	syncer := limitsync.New(store, keys, cfg.LiteLLMUser, cfg.LimitSyncWorkers)

	ui := handlers.NewUI(cfg, store, sessions, oidcProvider, keys, csrf, syncer.Kick)
	admin := handlers.NewAdmin(cfg, store, sessions, keys, csrf, syncer)

	// ── Router ────────────────────────────────────────────────────────────────
	r := newRouter(cfg, ui, admin, csrf, bunDB)

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

	// Revert expired profile assignments. Every 15 minutes rather than hourly:
	// a deadline an admin set for a particular date should take effect near
	// midnight, not up to an hour into the next day.
	expiryCtx, stopExpiry := context.WithCancel(context.Background())
	go profileexpiry.NewRunner(store, keys, syncer.Kick).Start(expiryCtx, 15*time.Minute)

	// Bring keys in line with their profiles. Runs at startup, whenever an
	// admin change asks for it, and on the interval to retry failed pushes.
	syncCtx, stopSync := context.WithCancel(context.Background())
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		syncer.Start(syncCtx, cfg.LimitSyncInterval)
	}()

	// Refresh key gauges alongside the other periodic work. Reading them from
	// the database keeps them correct across restarts.
	refreshGauges := func() {
		total, soon, err := store.CountAPIKeys(context.Background(), 7)
		if err != nil {
			slog.Error("metrics: count keys", "err", err)
			return
		}
		metrics.SetKeyGauges(total, soon)
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
		fatal("server", err)
	case <-quit:
		slog.Info("shutting down")
	}

	close(stopCleanup)
	stopReminder()
	stopExpiry()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown", "err", err)
	}

	// The sync stops after the push in flight. Wait for it so the database is
	// not closed under it; anything not reached stays pending for next time.
	stopSync()
	<-syncDone
}

// fatal logs err at error level and exits. log.Fatal would go through slog's
// default handler at INFO and hide the failure from level filters.
func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
