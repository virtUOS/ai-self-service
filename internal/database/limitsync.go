package database

import (
	"context"
	"time"
)

// --- Limit sync ---
//
// A key's limits upstream come from its owner's profile. When an admin edits
// a profile, moves a user, or changes which profile is the default, the keys
// affected are not pushed right away: there can be thousands, and each push is
// several gateway calls. Instead the database records what each key was last
// pushed with, and a background sync works through the keys that are out of
// date. The queries below decide which those are.

// pendingLimitSyncs selects every key whose upstream limits are out of date.
//
// A key's effective profile is its owner's own profile, or the default one for
// owners without one. The key is pending when that profile is not the one it
// was last pushed from, when the profile's limits changed since, or when there
// is no such profile at all. The last case is never pushed, since there are no
// limits to push; it stays pending so the sync reports it as a failure rather
// than leaving a key with whatever it had.
//
// IS NOT is SQLite's null-safe comparison, so a key that was never pushed
// (null columns) counts as different.
const pendingLimitSyncs = `
WITH effective AS (
	SELECT k.id AS key_id, k.litellm_key, k.key_prefix, k.user_id,
	       u.oidc_sub, u.email,
	       k.synced_profile_id, k.synced_rev, k.sync_error, k.sync_failed_at,
	       COALESCE(u.profile_id,
	                (SELECT id FROM profiles WHERE is_default <> 0 ORDER BY id LIMIT 1)) AS profile_id
	FROM api_keys AS k
	JOIN users AS u ON u.id = k.user_id
), pending AS (
	SELECT effective.*
	FROM effective
	LEFT JOIN profiles AS p ON p.id = effective.profile_id
	WHERE p.id IS NULL
	   OR effective.synced_profile_id IS NOT effective.profile_id
	   OR effective.synced_rev IS NOT p.limits_rev
)
`

// LimitSyncTarget is a key whose upstream limits are out of date, with what
// the sync needs to bring it up to date.
type LimitSyncTarget struct {
	KeyID      int64  `bun:"key_id"`
	LiteLLMKey string `bun:"litellm_key"`
	KeyPrefix  string `bun:"key_prefix"`
	UserID     int64  `bun:"user_id"`
	OIDCSub    string `bun:"oidc_sub"`
	Email      string `bun:"email"`

	// ProfileID is the owner's effective profile: their own, or the default.
	// Nil when they have none and no default exists.
	ProfileID *int64 `bun:"profile_id"`

	SyncError    string     `bun:"sync_error"`
	SyncFailedAt *time.Time `bun:"sync_failed_at"`
}

// PendingLimitSyncs returns up to limit out-of-date keys that have not failed
// since the given time.
//
// A sync run passes its own start time, so each key is tried at most once per
// run: a key that fails is skipped until the next run instead of being retried
// in a tight loop. A key that was pushed successfully during the run but went
// out of date again, because an admin edited its profile meanwhile, has no
// failure recorded and is picked up again by the same run.
//
// Keys that never failed come first, so one stubborn key cannot hold up the
// rest.
//
// bun stores times as UTC text ("2026-10-09 10:00:00.5+00:00"), which compares
// and sorts chronologically, so the filter and the order work on the text.
func (s *Store) PendingLimitSyncs(ctx context.Context, notFailedSince time.Time, limit int) ([]LimitSyncTarget, error) {
	var rows []LimitSyncTarget
	err := s.db.NewRaw(pendingLimitSyncs+`
		SELECT key_id, litellm_key, key_prefix, user_id, oidc_sub, email,
		       profile_id, sync_error, sync_failed_at
		FROM pending
		WHERE sync_failed_at IS NULL OR sync_failed_at < ?
		ORDER BY sync_failed_at IS NOT NULL, sync_failed_at, key_id
		LIMIT ?`, notFailedSince, limit).Scan(ctx, &rows)
	return rows, err
}

// MarkLimitsSynced records that a key now carries the limits of the given
// profile revision, and clears any earlier failure.
//
// The revision is the one the sync read before pushing, not the profile's
// current one: if an admin edited the profile while the push was in flight,
// the key must stay pending so the newer limits reach it too.
func (s *Store) MarkLimitsSynced(ctx context.Context, keyID, profileID, rev int64) error {
	_, err := s.db.NewUpdate().Model((*APIKey)(nil)).
		Set("synced_profile_id = ?", profileID).
		Set("synced_rev = ?", rev).
		Set("sync_error = ''").
		Set("sync_failed_at = NULL").
		Where("id = ?", keyID).Exec(ctx)
	return err
}

// MarkLimitSyncFailed records why a key could not be brought up to date. It
// stays pending.
func (s *Store) MarkLimitSyncFailed(ctx context.Context, keyID int64, reason string, at time.Time) error {
	_, err := s.db.NewUpdate().Model((*APIKey)(nil)).
		Set("sync_error = ?", reason).
		Set("sync_failed_at = ?", at).
		Where("id = ?", keyID).Exec(ctx)
	return err
}

// MarkAllLimitsPending marks every key as out of date, so the sync pushes the
// limits of each one again. This undoes changes made directly in the gateway,
// which the portal cannot see. It returns how many keys were marked.
func (s *Store) MarkAllLimitsPending(ctx context.Context) (int64, error) {
	res, err := s.db.NewUpdate().Model((*APIKey)(nil)).
		Set("synced_rev = NULL").
		Where("1 = 1").Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// LimitSyncFailure is a pending key whose last push failed.
type LimitSyncFailure struct {
	KeyPrefix string    `bun:"key_prefix"`
	Email     string    `bun:"email"`
	Error     string    `bun:"sync_error"`
	FailedAt  time.Time `bun:"sync_failed_at"`
}

// LimitSyncStatus summarises the keys whose limits are out of date.
type LimitSyncStatus struct {
	// Pending counts every out-of-date key, including the failed ones.
	Pending int
	// Failed counts the pending keys whose last push failed.
	Failed int
	// PendingByProfile counts pending keys per effective profile ID. Keys
	// whose owner has no profile at all are not in it.
	PendingByProfile map[int64]int
	// RecentFailures are the most recent failures, newest first.
	RecentFailures []LimitSyncFailure
}

// maxListedFailures caps the failures the admin panel lists. The count is
// always exact; the list is for spotting what went wrong.
const maxListedFailures = 20

// GetLimitSyncStatus reports how far the limit sync has got.
func (s *Store) GetLimitSyncStatus(ctx context.Context) (*LimitSyncStatus, error) {
	st := &LimitSyncStatus{PendingByProfile: map[int64]int{}}

	var byProfile []struct {
		ProfileID *int64 `bun:"profile_id"`
		Pending   int    `bun:"pending"`
		Failed    int    `bun:"failed"`
	}
	if err := s.db.NewRaw(pendingLimitSyncs+`
		SELECT profile_id, COUNT(*) AS pending, COUNT(sync_failed_at) AS failed
		FROM pending
		GROUP BY profile_id`).Scan(ctx, &byProfile); err != nil {
		return nil, err
	}
	for _, row := range byProfile {
		st.Pending += row.Pending
		st.Failed += row.Failed
		if row.ProfileID != nil {
			st.PendingByProfile[*row.ProfileID] = row.Pending
		}
	}

	if st.Failed > 0 {
		if err := s.db.NewRaw(pendingLimitSyncs+`
			SELECT key_prefix, email, sync_error, sync_failed_at
			FROM pending
			WHERE sync_failed_at IS NOT NULL
			ORDER BY sync_failed_at DESC, key_id
			LIMIT ?`, maxListedFailures).Scan(ctx, &st.RecentFailures); err != nil {
			return nil, err
		}
	}
	return st, nil
}
