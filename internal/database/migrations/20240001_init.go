package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS profiles (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				name            TEXT    NOT NULL UNIQUE,
				description     TEXT    NOT NULL DEFAULT '',
				models          TEXT    NOT NULL DEFAULT '[]',
				tpm_limit       INTEGER,
				rpm_limit       INTEGER,
				max_budget      REAL,
				budget_duration TEXT,
				is_default      INTEGER NOT NULL DEFAULT 0,
				created_at      DATETIME NOT NULL,
				updated_at      DATETIME NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("create profiles: %w", err)
		}

		_, err = db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS users (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				oidc_sub   TEXT    NOT NULL UNIQUE,
				email      TEXT    NOT NULL,
				name       TEXT    NOT NULL DEFAULT '',
				profile_id INTEGER REFERENCES profiles(id),
				created_at DATETIME NOT NULL,
				updated_at DATETIME NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("create users: %w", err)
		}

		_, err = db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS api_keys (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				litellm_key TEXT    NOT NULL,
				key_prefix  TEXT    NOT NULL,
				expires_at  DATETIME NOT NULL,
				created_at  DATETIME NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("create api_keys: %w", err)
		}

		_, err = db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS sessions (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				token      TEXT    NOT NULL UNIQUE,
				id_token   TEXT    NOT NULL,
				expires_at DATETIME NOT NULL,
				created_at DATETIME NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("create sessions: %w", err)
		}

		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS sessions`)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `DROP TABLE IF EXISTS api_keys`)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `DROP TABLE IF EXISTS users`)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `DROP TABLE IF EXISTS profiles`)
		return err
	})
}
