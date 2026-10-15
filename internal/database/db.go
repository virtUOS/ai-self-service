package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
	"github.com/virtuos/keyosk/internal/database/migrations"
)

type Store struct {
	db *bun.DB
}

func NewStore(db *bun.DB) *Store {
	return &Store{db: db}
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// ExecRaw runs a statement directly. Intended for tests that need to provoke
// database failures; production code should use the typed methods.
func (s *Store) ExecRaw(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// QueryRowRaw runs a query returning a single row. Intended for tests that
// need to inspect rows the typed methods do not expose.
func (s *Store) QueryRowRaw(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

// newMigrator records a migration as applied only once it has succeeded. By
// default bun records it first, so a migration that failed halfway would be
// skipped on the next start.
func newMigrator(db *bun.DB, ms *migrate.Migrations) *migrate.Migrator {
	return migrate.NewMigrator(db, ms, migrate.WithMarkAppliedOnSuccess(true))
}

func (s *Store) RunMigrations(ctx context.Context) error {
	migrator := newMigrator(s.db, migrations.Migrations)
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

	for _, m := range group.Migrations {
		if m.Name == migrations.KeyRefName {
			s.scrubFreePages(ctx)
			break
		}
	}
	return nil
}

// scrubFreePages rewrites the database file so that nothing a migration
// overwrote survives in free pages, and truncates the WAL, which would hold
// copies of the pages otherwise.
//
// A failure is logged, not returned: the migration is committed and will not
// run again, and refusing to start would not bring the scrub back either.
func (s *Store) scrubFreePages(ctx context.Context) {
	if err := s.scrub(ctx); err != nil {
		slog.Warn("could not scrub old plaintext keys from the database file; run VACUUM on it by hand",
			"err", err)
	}
}

// scrub runs VACUUM and truncates the WAL.
//
// A checkpoint that another connection keeps from finishing, by still reading
// an older snapshot, is not an error to SQLite: it reports it in the first
// column of the pragma's result row. The vacuumed pages then stay in the WAL
// and the old ones in the file, so that counts as a failure here.
func (s *Store) scrub(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("VACUUM: %w", err)
	}
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("wal_checkpoint: %w", err)
	}
	if busy != 0 {
		return errors.New("wal_checkpoint: another connection kept it from finishing")
	}
	return nil
}

// SeedDefaultProfile makes sure a default profile exists.
//
// A fresh database gets an unlimited profile named "default". A database with
// profiles but no default, left by a version that let the default be removed
// or by a hand edit, has the profile named "default" promoted instead, since
// inserting another would collide with its name. If there is none, it refuses
// rather than add an unlimited default that every unassigned user would get.
func (s *Store) SeedDefaultProfile(ctx context.Context) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		hasDefault, err := tx.NewSelect().Model((*Profile)(nil)).
			Where("is_default <> 0").Exists(ctx)
		if err != nil || hasDefault {
			return err
		}

		profiles, err := tx.NewSelect().Model((*Profile)(nil)).Count(ctx)
		if err != nil {
			return err
		}
		if profiles == 0 {
			now := time.Now()
			_, err := tx.NewInsert().Model(&Profile{
				Name:        "default",
				Description: "Default profile — no rate or budget limits.",
				IsDefault:   true,
				LimitsRev:   1,
				CreatedAt:   now,
				UpdatedAt:   now,
			}).Value("models", "?", "[]").Exec(ctx)
			return err
		}

		res, err := tx.NewUpdate().Model((*Profile)(nil)).
			Set("is_default = ?", true).
			Set("updated_at = ?", time.Now()).
			Where("name = ?", "default").Exec(ctx)
		if err != nil {
			return fmt.Errorf("promote profile %q: %w", "default", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.New(`no default profile, and none named "default" to promote: ` +
				`mark one as default, e.g. UPDATE profiles SET is_default = 1 WHERE name = '<name>'`)
		}
		slog.Warn(`no default profile; promoted the profile named "default"`)
		return nil
	})
}

// --- Profiles ---

// ErrDefaultProfile is returned for a change that would leave no default
// profile: deleting the default, or saving it as not the default. Users with no
// profile of their own get their keys from the default, so without one key
// generation, the limit sync and the profile-expiry job all fail. The default
// moves by marking another profile as default instead.
var ErrDefaultProfile = errors.New("the default profile cannot be deleted or un-defaulted")

// ProfileInUseError is returned for deleting a profile that users are still
// assigned to. They would otherwise be left pointing at a missing profile,
// which the foreign key rejects with an error that names neither cause nor fix.
type ProfileInUseError struct {
	Users int
}

func (e *ProfileInUseError) Error() string {
	return fmt.Sprintf("profile is assigned to %d user(s)", e.Users)
}

// ListProfiles returns every profile with its quota windows.
//
// The relation is loaded here, not left to the caller: the admin table and the
// edit form both render straight from this, so a profile without its windows
// silently reads as unlimited.
func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	var profiles []Profile
	err := s.db.NewSelect().Model(&profiles).
		Relation("Quotas").
		OrderExpr("is_default DESC, name ASC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *Store) GetProfile(ctx context.Context, id int64) (*Profile, error) {
	p := &Profile{}
	err := s.db.NewSelect().Model(p).Relation("Quotas").Where("profile.id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// setProfileQuotasTx replaces a profile's allowance windows.
//
// Replace rather than merge: a window an admin removed must stop being
// enforced. It runs in the transaction that saves the profile, so a profile is
// never saved without its windows, which would read as unlimited.
//
// A set that differs from the stored one raises the profile's LimitsRev, which
// marks every key on the profile for the limit sync. Saving the same windows
// again does not, so re-saving a profile for an unrelated edit does not push
// to every key it covers.
func setProfileQuotasTx(ctx context.Context, tx bun.Tx, profileID int64, quotas []ProfileQuota) error {
	rows := make([]ProfileQuota, 0, len(quotas))
	for _, q := range quotas {
		if q.Budget <= 0 || q.Period == "" {
			continue
		}
		q.ID, q.ProfileID = 0, profileID
		rows = append(rows, q)
	}

	var current []ProfileQuota
	if err := tx.NewSelect().Model(&current).
		Where("profile_id = ?", profileID).Scan(ctx); err != nil {
		return fmt.Errorf("read profile quotas: %w", err)
	}
	if sameQuotaWindows(current, rows) {
		return nil
	}

	if _, err := tx.NewDelete().Model((*ProfileQuota)(nil)).
		Where("profile_id = ?", profileID).Exec(ctx); err != nil {
		return fmt.Errorf("clear profile quotas: %w", err)
	}
	if len(rows) > 0 {
		if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
			return fmt.Errorf("insert profile quotas: %w", err)
		}
	}
	if _, err := tx.NewUpdate().Model((*Profile)(nil)).
		Set("limits_rev = limits_rev + 1").
		Where("id = ?", profileID).Exec(ctx); err != nil {
		return fmt.Errorf("bump limits revision: %w", err)
	}
	return nil
}

// sameQuotaWindows reports whether two sets of windows enforce the same thing,
// regardless of order.
func sameQuotaWindows(a, b []ProfileQuota) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(q ProfileQuota) string { return fmt.Sprintf("%s/%g", q.Period, q.Budget) }
	counts := make(map[string]int, len(a))
	for _, q := range a {
		counts[key(q)]++
	}
	for _, q := range b {
		if counts[key(q)] == 0 {
			return false
		}
		counts[key(q)]--
	}
	return true
}

// GetDefaultProfile returns the fallback profile, with its quota windows.
//
// Users with no profile of their own are issued keys from this one, so
// dropping the windows here would issue keys that enforce no quota at all.
func (s *Store) GetDefaultProfile(ctx context.Context) (*Profile, error) {
	p := &Profile{}
	err := s.db.NewSelect().Model(p).
		Relation("Quotas").
		Where("is_default <> 0").Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CreateProfile inserts a profile with its quota windows, in one transaction:
// a failure at either step saves nothing. Marking it default demotes the
// previous default, since the database permits only one, and returns the names
// of the profiles it demoted, which the audit log records.
func (s *Store) CreateProfile(ctx context.Context, p *Profile, quotas []ProfileQuota) (demoted []string, err error) {
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		if demoted, err = demotedDefaults(ctx, tx, p); err != nil {
			return err
		}
		if err := createProfileTx(ctx, tx, p); err != nil {
			return err
		}
		return setProfileQuotasTx(ctx, tx, p.ID, quotas)
	})
	if err != nil {
		return nil, err
	}
	return demoted, nil
}

func createProfileTx(ctx context.Context, tx bun.Tx, p *Profile) error {
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.LimitsRev == 0 {
		p.LimitsRev = 1
	}
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return err
	}
	if p.IsDefault {
		if err := clearDefaultProfile(ctx, tx, 0); err != nil {
			return err
		}
	}
	_, err = tx.NewInsert().Model(p).
		Value("models", "?", string(modelsJSON)).
		Exec(ctx)
	return err
}

// demotedDefaults returns the names of the profiles that saving p demotes
// from default: none unless p is marked as the default.
func demotedDefaults(ctx context.Context, tx bun.Tx, p *Profile) ([]string, error) {
	if !p.IsDefault {
		return nil, nil
	}
	var names []string
	if err := tx.NewSelect().Model((*Profile)(nil)).Column("name").
		Where("is_default <> 0 AND id <> ?", p.ID).
		Order("id").Scan(ctx, &names); err != nil {
		return nil, fmt.Errorf("read default profile: %w", err)
	}
	return names, nil
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

// UpdateProfile saves an existing profile and replaces its quota windows, in
// one transaction: a failure at either step saves nothing. It never creates a
// profile; for an ID that does not exist it returns an error wrapping
// sql.ErrNoRows. Marking the profile default demotes any other default, and
// saving the default as not the default returns ErrDefaultProfile.
//
// A change to the models or the TPM/RPM limits raises LimitsRev, as does a
// change to the windows, which marks every key on the profile for the limit
// sync.
//
// It returns the profile as it was before, with its windows, read in the same
// transaction, so the audit log can say what changed, and the names of the
// profiles the save demoted from default, which the log records as well.
func (s *Store) UpdateProfile(ctx context.Context, p *Profile, quotas []ProfileQuota) (prev *Profile, demoted []string, err error) {
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		if demoted, err = demotedDefaults(ctx, tx, p); err != nil {
			return err
		}
		if prev, err = updateProfileTx(ctx, tx, p); err != nil {
			return err
		}
		return setProfileQuotasTx(ctx, tx, p.ID, quotas)
	})
	if err != nil {
		return nil, nil, err
	}
	return prev, demoted, nil
}

// updateProfileTx saves p and returns the profile as it was before, with its
// quota windows.
func updateProfileTx(ctx context.Context, tx bun.Tx, p *Profile) (*Profile, error) {
	p.UpdatedAt = time.Now()
	modelsJSON, err := json.Marshal(p.Models)
	if err != nil {
		return nil, err
	}
	current := &Profile{}
	if err := tx.NewSelect().Model(current).Relation("Quotas").
		Where("profile.id = ?", p.ID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	if current.IsDefault && !p.IsDefault {
		return nil, ErrDefaultProfile
	}
	p.LimitsRev = current.LimitsRev
	if !slices.Equal(current.Models, p.Models) ||
		!sameLimit(current.TPMLimit, p.TPMLimit) ||
		!sameLimit(current.RPMLimit, p.RPMLimit) {
		p.LimitsRev++
	}

	if p.IsDefault {
		if err := clearDefaultProfile(ctx, tx, p.ID); err != nil {
			return nil, err
		}
	}
	_, err = tx.NewUpdate().Model(p).
		Set("name = ?", p.Name).
		Set("description = ?", p.Description).
		Set("models = ?", string(modelsJSON)).
		Set("tpm_limit = ?", p.TPMLimit).
		Set("rpm_limit = ?", p.RPMLimit).
		Set("max_budget = ?", p.MaxBudget).
		Set("budget_duration = ?", p.BudgetDuration).
		Set("key_duration_days = ?", p.KeyDurationDays).
		Set("is_default = ?", p.IsDefault).
		Set("limits_rev = ?", p.LimitsRev).
		Set("updated_at = ?", p.UpdatedAt).
		Where("id = ?", p.ID).
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	return current, nil
}

// sameLimit compares two optional limits, where nil means unlimited.
func sameLimit(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// DeleteProfile removes a profile and the quota windows belonging to it. It
// refuses to remove the default profile with ErrDefaultProfile, and one that
// users are assigned to with a *ProfileInUseError.
//
// Open enables foreign keys, so ON DELETE CASCADE would remove the windows on
// its own. They are still deleted explicitly so that a handle opened without
// the pragma, as the tests do, does not leave orphaned rows behind for a
// reused profile id to inherit.
func (s *Store) DeleteProfile(ctx context.Context, id int64) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		isDefault, err := tx.NewSelect().Model((*Profile)(nil)).
			Where("id = ? AND is_default <> 0", id).Exists(ctx)
		if err != nil {
			return fmt.Errorf("read profile: %w", err)
		}
		if isDefault {
			return ErrDefaultProfile
		}
		assigned, err := tx.NewSelect().Model((*User)(nil)).
			Where("profile_id = ?", id).Count(ctx)
		if err != nil {
			return fmt.Errorf("count assigned users: %w", err)
		}
		if assigned > 0 {
			return &ProfileInUseError{Users: int(assigned)}
		}

		if _, err := tx.NewDelete().Model((*ProfileQuota)(nil)).
			Where("profile_id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("delete profile quotas: %w", err)
		}
		// A user due to land on this profile would otherwise keep a dangling
		// reference. They fall back to the default, which is what an unset
		// destination has always meant. The deadline itself stays.
		if _, err := tx.NewUpdate().Model((*User)(nil)).
			Set("profile_after_expiry = NULL").
			Where("profile_after_expiry = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("clear pending profile destinations: %w", err)
		}
		_, err = tx.NewDelete().Model((*Profile)(nil)).Where("id = ?", id).Exec(ctx)
		return err
	})
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

// SetUserProfileUntil assigns a profile, optionally with a deadline.
//
// A nil expiresAt makes the assignment permanent and clears any deadline that
// was set, so an admin can take a deadline off by clearing the date field. A
// nil afterExpiry means the user falls back to the default profile when the
// deadline passes — it is resolved then rather than now, so the row does not
// go stale if the default changes in between.
func (s *Store) SetUserProfileUntil(ctx context.Context, userID int64, profileID *int64,
	expiresAt *time.Time, afterExpiry *int64, revokeKey bool) error {

	// A deadline is what makes the other two fields meaningful; without one
	// they would sit in the row describing an expiry that never comes.
	if expiresAt == nil {
		afterExpiry, revokeKey = nil, false
	}
	_, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_id = ?", profileID).
		Set("profile_expires_at = ?", expiresAt).
		Set("profile_after_expiry = ?", afterExpiry).
		Set("revoke_key_at_expiry = ?", revokeKey).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).Exec(ctx)
	return err
}

// UsersWithPassedDeadline returns users whose assignment has run out.
//
// Takes the time rather than reading the clock so a test can place a deadline
// either side of it without sleeping.
func (s *Store) UsersWithPassedDeadline(ctx context.Context, now time.Time) ([]User, error) {
	var users []User
	err := s.db.NewSelect().Model(&users).
		Where("profile_expires_at IS NOT NULL AND profile_expires_at <= ?", now).
		OrderExpr("id ASC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return users, nil
}

// ApplyProfileExpiry moves a user to their post-deadline profile and clears the
// deadline.
//
// One transaction: a user left with a passed deadline and the old profile still
// attached would be reverted again on the next run, and a user whose deadline
// was cleared without the profile moving would keep the elevated limits for
// good. A nil newProfileID means the default profile.
func (s *Store) ApplyProfileExpiry(ctx context.Context, userID int64, newProfileID *int64) error {
	_, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_id = ?", newProfileID).
		Set("profile_expires_at = NULL").
		Set("profile_after_expiry = NULL").
		Set("revoke_key_at_expiry = ?", false).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).Exec(ctx)
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

// UpdateAPIKeyExpiry moves a key's expiry date and clears the expiry notices
// already sent for it. Those notices were about the old date; leaving them
// would make the reminder job skip every warning for the new one.
func (s *Store) UpdateAPIKeyExpiry(ctx context.Context, keyID int64, expiresAt time.Time) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model((*APIKey)(nil)).
			Set("expires_at = ?", expiresAt).
			Where("id = ?", keyID).
			Exec(ctx); err != nil {
			return fmt.Errorf("update expiry: %w", err)
		}
		if _, err := tx.NewDelete().Model((*ExpiryNotice)(nil)).
			Where("api_key_id = ?", keyID).Exec(ctx); err != nil {
			return fmt.Errorf("clear expiry notices: %w", err)
		}
		return nil
	})
}

func (s *Store) DeleteAPIKey(ctx context.Context, keyID int64) error {
	_, err := s.db.NewDelete().Model((*APIKey)(nil)).Where("id = ?", keyID).Exec(ctx)
	return err
}

// RemoveMissingKey drops the record of a key that no longer exists in the
// gateway, and records that in the audit log. Such a key was deleted outside
// the portal; it cannot be restored, so keeping the row would only leave the
// owner with a key that does not work and the sync with one it cannot push.
// The owner can generate a new key.
//
// It reports whether it removed anything: another request may have dropped
// the row first, and then there is nothing to record.
func (s *Store) RemoveMissingKey(ctx context.Context, keyID, ownerID int64, ownerEmail, detail string) (bool, error) {
	removed := false
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*APIKey)(nil)).Where("id = ?", keyID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("delete key row: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		removed = true
		_, err = tx.NewInsert().Model(&AuditEvent{
			CreatedAt:    time.Now(),
			Action:       AuditKeyMissing,
			ActorEmail:   "system", // nobody in the portal deleted it
			SubjectEmail: ownerEmail,
			SubjectID:    &ownerID,
			Detail:       detail,
		}).Exec(ctx)
		return err
	})
	return removed, err
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

// --- Audit log ---

// RecordAudit appends an audit event. Failures are returned but callers should
// treat them as non-fatal: losing an audit row must not fail the user's action.
func (s *Store) RecordAudit(ctx context.Context, e *AuditEvent) error {
	e.CreatedAt = time.Now()
	_, err := s.db.NewInsert().Model(e).Exec(ctx)
	return err
}

// ListAuditEvents returns the most recent events, newest first.
func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var events []AuditEvent
	err := s.db.NewSelect().Model(&events).
		OrderExpr("created_at DESC, id DESC").
		Limit(int64(limit)).
		Scan(ctx)
	return events, err
}

// ListAPIKeys returns every stored key, for the admin overview.
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	var keys []APIKey
	err := s.db.NewSelect().Model(&keys).Scan(ctx)
	return keys, err
}

// CountAPIKeys returns how many keys are stored and how many of them expire
// within the next withinDays days. Already-expired keys count towards the total
// only.
func (s *Store) CountAPIKeys(ctx context.Context, withinDays int) (total, expiring int, err error) {
	now := time.Now()
	all, err := s.db.NewSelect().Model((*APIKey)(nil)).Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	soon, err := s.db.NewSelect().Model((*APIKey)(nil)).
		Where("expires_at > ?", now).
		Where("expires_at <= ?", now.AddDate(0, 0, withinDays)).
		Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	return int(all), int(soon), nil
}

// --- Expiry notifications ---

// KeysExpiringWithin returns keys expiring in the next window that have not yet
// had a notice recorded for daysBefore, joined to their owner.
//
// Already-expired keys are excluded: a warning after the fact is noise, and the
// user has already discovered the problem.
func (s *Store) KeysExpiringWithin(ctx context.Context, daysBefore int) ([]ExpiringKey, error) {
	cutoff := time.Now().AddDate(0, 0, daysBefore)

	var rows []ExpiringKey
	err := s.db.NewSelect().
		Model((*APIKey)(nil)).
		ColumnExpr("api_key.*").
		ColumnExpr("u.email AS email").
		ColumnExpr("u.name AS name").
		Join("JOIN users AS u ON u.id = api_key.user_id").
		Where("api_key.expires_at <= ?", cutoff).
		Where("api_key.expires_at > ?", time.Now()).
		Where(`NOT EXISTS (
			SELECT 1 FROM expiry_notices n
			WHERE n.api_key_id = api_key.id AND n.days_before = ?
		)`, daysBefore).
		Scan(ctx, &rows)
	return rows, err
}

// MarkExpiryNoticeSent records a delivered warning. The unique index makes this
// the point at which a concurrent duplicate is rejected.
func (s *Store) MarkExpiryNoticeSent(ctx context.Context, keyID int64, daysBefore int) error {
	_, err := s.db.NewInsert().Model(&ExpiryNotice{
		APIKeyID:   keyID,
		DaysBefore: daysBefore,
		SentAt:     time.Now(),
	}).Exec(ctx)
	return err
}
