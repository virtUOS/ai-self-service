package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
	"github.com/virtuos/ai-self-service/internal/database/migrations"
)

type Store struct {
	db *bun.DB
}

func NewStore(db *bun.DB) *Store {
	return &Store{db: db}
}

func (s *Store) RunMigrations(ctx context.Context) error {
	migrator := migrate.NewMigrator(s.db, migrations.Migrations)
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	if group.IsZero() {
		return nil
	}
	fmt.Printf("migrated to %s\n", group)
	return nil
}

func (s *Store) SeedDefaultProfile(ctx context.Context) error {
	count, err := s.db.NewSelect().Model((*Profile)(nil)).Where("is_default <> 0").Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	now := time.Now()
	p := &Profile{
		Name:        "default",
		Description: "Default profile — no rate or budget limits.",
		Models:      []string{},
		IsDefault:   true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return s.upsertProfile(ctx, p)
}

func (s *Store) upsertProfile(ctx context.Context, p *Profile) error {
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return err
	}
	_, err = s.db.NewInsert().Model(p).
		Value("models", "?", string(modelsJSON)).
		Exec(ctx)
	return err
}

// --- Profiles ---

func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	var profiles []Profile
	err := s.db.NewSelect().Model(&profiles).OrderExpr("is_default DESC, name ASC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *Store) GetProfile(ctx context.Context, id int64) (*Profile, error) {
	p := &Profile{}
	err := s.db.NewSelect().Model(p).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) GetDefaultProfile(ctx context.Context) (*Profile, error) {
	p := &Profile{}
	err := s.db.NewSelect().Model(p).Where("is_default <> 0").Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CreateProfile inserts a profile. Marking it default demotes the previous
// default in the same transaction, since the database permits only one.
func (s *Store) CreateProfile(ctx context.Context, p *Profile) error {
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return err
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if p.IsDefault {
			if err := clearDefaultProfile(ctx, tx, 0); err != nil {
				return err
			}
		}
		_, err := tx.NewInsert().Model(p).
			Value("models", "?", string(modelsJSON)).
			Exec(ctx)
		return err
	})
}

// clearDefaultProfile demotes every default profile except keepID (0 keeps none).
func clearDefaultProfile(ctx context.Context, tx bun.Tx, keepID int64) error {
	q := tx.NewUpdate().Model((*Profile)(nil)).
		Set("is_default = ?", false).
		Where("is_default <> 0")
	if keepID != 0 {
		q = q.Where("id <> ?", keepID)
	}
	_, err := q.Exec(ctx)
	return err
}

// UpdateProfile saves a profile, demoting any other default when this one is
// marked as such.
func (s *Store) UpdateProfile(ctx context.Context, p *Profile) error {
	p.UpdatedAt = time.Now()
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return err
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if p.IsDefault {
			if err := clearDefaultProfile(ctx, tx, p.ID); err != nil {
				return err
			}
		}
		return s.updateProfileTx(ctx, tx, p, string(modelsJSON))
	})
}

func (s *Store) updateProfileTx(ctx context.Context, tx bun.Tx, p *Profile, modelsJSON string) error {
	_, err := tx.NewUpdate().Model(p).
		Set("name = ?", p.Name).
		Set("description = ?", p.Description).
		Set("models = ?", modelsJSON).
		Set("tpm_limit = ?", p.TPMLimit).
		Set("rpm_limit = ?", p.RPMLimit).
		Set("max_budget = ?", p.MaxBudget).
		Set("budget_duration = ?", p.BudgetDuration).
		Set("key_duration_days = ?", p.KeyDurationDays).
		Set("quota_tokens = ?", p.QuotaTokens).
		Set("quota_period = ?", p.QuotaPeriod).
		Set("is_default = ?", p.IsDefault).
		Set("updated_at = ?", p.UpdatedAt).
		Where("id = ?", p.ID).
		Exec(ctx)
	return err
}

func (s *Store) DeleteProfile(ctx context.Context, id int64) error {
	_, err := s.db.NewDelete().Model((*Profile)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// --- Users ---

func (s *Store) GetOrCreateUser(ctx context.Context, sub, email, name string) (*User, error) {
	user := &User{}
	err := s.db.NewSelect().Model(user).Where("oidc_sub = ?", sub).Scan(ctx)
	if err == nil {
		// Update name/email in case they changed
		user.Email = email
		user.Name = name
		user.UpdatedAt = time.Now()
		_, _ = s.db.NewUpdate().Model(user).
			Set("email = ?", email).
			Set("name = ?", name).
			Set("updated_at = ?", user.UpdatedAt).
			Where("id = ?", user.ID).Exec(ctx)
		return user, nil
	}

	now := time.Now()
	user = &User{
		OIDCSub:   sub,
		Email:     email,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = s.db.NewInsert().Model(user).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	user := &User{}
	err := s.db.NewSelect().Model(user).
		Where("id = ?", id).
		Scan(ctx)
	return user, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	if err := s.db.NewSelect().Model(&users).OrderExpr("email ASC").Scan(ctx); err != nil {
		return nil, err
	}
	// Collect unique profile IDs and load them in one query.
	var profileIDs []int64
	for _, u := range users {
		if u.ProfileID != nil {
			profileIDs = append(profileIDs, *u.ProfileID)
		}
	}
	if len(profileIDs) > 0 {
		var profiles []Profile
		if err := s.db.NewSelect().Model(&profiles).
			Where("id IN (?)", bun.In(profileIDs)).
			Scan(ctx); err != nil {
			return nil, err
		}
		pm := make(map[int64]*Profile, len(profiles))
		for i := range profiles {
			pm[profiles[i].ID] = &profiles[i]
		}
		for i := range users {
			if users[i].ProfileID != nil {
				users[i].Profile = pm[*users[i].ProfileID]
			}
		}
	}
	return users, nil
}

func (s *Store) SetUserProfile(ctx context.Context, userID int64, profileID *int64) error {
	_, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_id = ?", profileID).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).
		Exec(ctx)
	return err
}

func (s *Store) GetUserByOIDCSub(ctx context.Context, sub string) (*User, error) {
	user := &User{}
	err := s.db.NewSelect().Model(user).Where("oidc_sub = ?", sub).Scan(ctx)
	return user, err
}

// --- API Keys ---

// GetAPIKeyByUser returns the user's key, or (nil, nil) when they have none.
// A missing key is an ordinary state here, not an error.
func (s *Store) GetAPIKeyByUser(ctx context.Context, userID int64) (*APIKey, error) {
	key := &APIKey{}
	err := s.db.NewSelect().Model(key).Where("user_id = ?", userID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return key, nil
}

// ReplaceAPIKey atomically swaps a user's key row: the old row (if any) is
// removed and the new one inserted in one transaction. The unique index on
// user_id means a non-transactional delete-then-insert could otherwise leave
// the user with no key at all if the insert failed.
func (s *Store) ReplaceAPIKey(ctx context.Context, k *APIKey) error {
	k.CreatedAt = time.Now()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*APIKey)(nil)).
			Where("user_id = ?", k.UserID).Exec(ctx); err != nil {
			return fmt.Errorf("remove previous key: %w", err)
		}
		if _, err := tx.NewInsert().Model(k).Exec(ctx); err != nil {
			return fmt.Errorf("insert new key: %w", err)
		}
		return nil
	})
}

func (s *Store) CreateAPIKey(ctx context.Context, k *APIKey) error {
	k.CreatedAt = time.Now()
	_, err := s.db.NewInsert().Model(k).Exec(ctx)
	return err
}

func (s *Store) UpdateAPIKeyExpiry(ctx context.Context, keyID int64, expiresAt time.Time) error {
	_, err := s.db.NewUpdate().Model((*APIKey)(nil)).
		Set("expires_at = ?", expiresAt).
		Where("id = ?", keyID).
		Exec(ctx)
	return err
}

func (s *Store) DeleteAPIKey(ctx context.Context, keyID int64) error {
	_, err := s.db.NewDelete().Model((*APIKey)(nil)).Where("id = ?", keyID).Exec(ctx)
	return err
}

// --- Sessions ---

func (s *Store) CreateSession(ctx context.Context, userID int64, token, idToken string, expiresAt time.Time) error {
	sess := &Session{
		UserID:    userID,
		Token:     token,
		IDToken:   idToken,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	_, err := s.db.NewInsert().Model(sess).Exec(ctx)
	return err
}

func (s *Store) GetSessionByToken(ctx context.Context, token string) (*Session, error) {
	sess := &Session{}
	err := s.db.NewSelect().Model(sess).Where("token = ?", token).Scan(ctx)
	return sess, err
}

func (s *Store) DeleteSession(ctx context.Context, sessionID int64) error {
	_, err := s.db.NewDelete().Model((*Session)(nil)).Where("id = ?", sessionID).Exec(ctx)
	return err
}

func (s *Store) DeleteSessionsByOIDCSub(ctx context.Context, sub string) error {
	_, err := s.db.NewDelete().TableExpr("sessions").
		Where("user_id IN (SELECT id FROM users WHERE oidc_sub = ?)", sub).
		Exec(ctx)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.NewDelete().Model((*Session)(nil)).
		Where("expires_at < ?", time.Now()).
		Exec(ctx)
	return err
}

// GetSessionIDToken returns the OIDC id_token for a session (used for OIDC logout).
func (s *Store) GetSessionIDToken(ctx context.Context, sessionID int64) (string, error) {
	sess := &Session{}
	err := s.db.NewSelect().Model(sess).Where("id = ?", sessionID).Scan(ctx)
	if err != nil {
		return "", err
	}
	return sess.IDToken, nil
}
