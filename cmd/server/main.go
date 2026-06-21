package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
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
	var bunDB *bun.DB
	switch cfg.DBType {
	case "postgres":
		sqldb, err := sql.Open("postgres", cfg.DBDSN)
		if err != nil {
			log.Fatalf("open postgres: %v", err)
		}
		bunDB = bun.NewDB(sqldb, pgdialect.New())
	default:
		sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+cfg.DBPath+"?cache=shared&_foreign_keys=on")
		if err != nil {
			log.Fatalf("open sqlite: %v", err)
		}
		bunDB = bun.NewDB(sqldb, sqlitedialect.New())
	}
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
	llClient := litellm.NewClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey)

	ui := handlers.NewUI(cfg, store, sessions, oidcProvider, llClient)
	admin := handlers.NewAdmin(cfg, store, sessions)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS))))

	r.Get("/login", ui.Login)
	r.Get("/callback", ui.Callback)
	r.Post("/logout", ui.Logout)
	r.Post("/backchannel-logout", ui.BackchannelLogout)
	r.Get("/session/status", ui.SessionStatus)

	r.Get("/", ui.Dashboard)
	r.Post("/key/generate", ui.GenerateKey)
	r.Post("/key/extend", ui.ExtendKey)
	r.Post("/key/delete", ui.DeleteKey)

	r.Route("/admin", func(r chi.Router) {
		r.Use(admin.Middleware)
		r.Get("/", admin.Panel)
		r.Post("/profiles", admin.CreateProfile)
		r.Post("/profiles/{id}", admin.UpdateProfile)
		r.Post("/profiles/{id}/delete", admin.DeleteProfile)
		r.Post("/users/{id}/profile", admin.SetUserProfile)
	})

	log.Printf("listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}
