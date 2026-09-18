package database

import (
	"context"
	"testing"
	"time"
)

// Every user that exists today has a permanent assignment. The new columns
// must default to that, or an upgrade would start expiring people.
func TestExistingUsersHaveNoDeadline(t *testing.T) {
	s := migratedStore(t, "pexp1")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-a", "a@uni-osnabrueck.de", "A")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt != nil {
		t.Errorf("ProfileExpiresAt = %v, want nil for a new user", got.ProfileExpiresAt)
	}
	if got.ProfileAfterExpiry != nil {
		t.Errorf("ProfileAfterExpiry = %v, want nil", got.ProfileAfterExpiry)
	}
	if got.RevokeKeyAtExpiry {
		t.Error("RevokeKeyAtExpiry = true, want false")
	}
}

// The three columns must round-trip, including the nullable destination.
func TestDeadlineColumnsRoundTrip(t *testing.T) {
	s := migratedStore(t, "pexp2")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-b", "b@uni-osnabrueck.de", "B")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	dest := int64(7)
	if _, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("profile_expires_at = ?", deadline).
		Set("profile_after_expiry = ?", dest).
		Set("revoke_key_at_expiry = ?", true).
		Where("id = ?", u.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt == nil || !got.ProfileExpiresAt.UTC().Truncate(time.Second).Equal(deadline) {
		t.Errorf("ProfileExpiresAt = %v, want %v", got.ProfileExpiresAt, deadline)
	}
	if got.ProfileAfterExpiry == nil || *got.ProfileAfterExpiry != dest {
		t.Errorf("ProfileAfterExpiry = %v, want %d", got.ProfileAfterExpiry, dest)
	}
	if !got.RevokeKeyAtExpiry {
		t.Error("RevokeKeyAtExpiry = false, want true")
	}
}
