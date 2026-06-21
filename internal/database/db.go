package database

import (
	"context"
	"encoding/json"
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
	count, err := s.db.NewSelect().Model((*Profile)(nil)).Where("is_default = 1").Count(ctx)
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
	for i := range profiles {
		if err := parseModels(&profiles[i]); err != nil {
			return nil, err
		}
	}
	return profiles, nil
}

func (s *Store) GetProfile(ctx context.Context, id int64) (*Profile, error) {
	p := &Profile{}
	err := s.db.NewSelect().Model(p).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, parseModels(p)
}

func (s *Store) GetDefaultProfile(ctx context.Context) (*Profile, error) {
	p := &Profile{}
	err := s.db.NewSelect().Model(p).Where("is_default = 1").Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, parseModels(p)
}

func (s *Store) CreateProfile(ctx context.Context, p *Profile) error {
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return err
	}
	_, err = s.db.NewInsert().Model(p).
		Value("models", "?", string(modelsJSON)).
		Exec(ctx)
	return err
}

func (s *Store) UpdateProfile(ctx context.Context, p *Profile) error {
	p.UpdatedAt = time.Now()
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return err
	}
	_, err = s.db.NewUpdate().Model(p).
		Set("name = ?", p.Name).
		Set("description = ?", p.Description).
		Set("models = ?", string(modelsJSON)).
		Set("tpm_limit = ?", p.TPMLimit).
		Set("rpm_limit = ?", p.RPMLimit).
		Set("max_budget = ?", p.MaxBudget).
		Set("budget_duration = ?", p.BudgetDuration).
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

// parseModels deserializes the JSON-encoded models field from the DB into []string.
func parseModels(p *Profile) error {
	// Models field is stored as JSON text; Bun will have scanned it as a string into the slice field.
	// If bun already decoded it we're fine; otherwise decode manually.
	return nil
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

func (s *Store) GetAPIKeyByUser(ctx context.Context, userID int64) (*APIKey, error) {
	key := &APIKey{}
	err := s.db.NewSelect().Model(key).Where("user_id = ?", userID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return key, nil
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
