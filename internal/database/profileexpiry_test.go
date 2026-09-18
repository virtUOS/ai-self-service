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

// Only deadlines that have actually passed are returned, or the job would
// revert people early.
func TestUsersWithPassedDeadlineIgnoresFutureAndNull(t *testing.T) {
	s := migratedStore(t, "pexp3")
	ctx := context.Background()
	now := time.Now()

	past, err := s.GetOrCreateUser(ctx, "sub-past", "past@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	future, err := s.GetOrCreateUser(ctx, "sub-future", "future@uni-osnabrueck.de", "F")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrCreateUser(ctx, "sub-none", "none@uni-osnabrueck.de", "N"); err != nil {
		t.Fatal(err)
	}

	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	if err := s.SetUserProfileUntil(ctx, past.ID, nil, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserProfileUntil(ctx, future.ID, nil, &tomorrow, nil, false); err != nil {
		t.Fatal(err)
	}

	due, err := s.UsersWithPassedDeadline(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("got %d users due, want 1: %+v", len(due), due)
	}
	if due[0].ID != past.ID {
		t.Errorf("due user = %d, want the one whose deadline passed (%d)", due[0].ID, past.ID)
	}
}

// Applying an expiry must clear the deadline, or the job would fire again on
// every run for the same user.
func TestApplyProfileExpiryClearsTheDeadline(t *testing.T) {
	s := migratedStore(t, "pexp4")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-c", "c@uni-osnabrueck.de", "C")
	if err != nil {
		t.Fatal(err)
	}
	dest := &Profile{Name: "after"}
	if err := s.CreateProfile(ctx, dest); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := s.SetUserProfileUntil(ctx, u.ID, nil, &yesterday, &dest.ID, true); err != nil {
		t.Fatal(err)
	}

	if err := s.ApplyProfileExpiry(ctx, u.ID, &dest.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID == nil || *got.ProfileID != dest.ID {
		t.Errorf("ProfileID = %v, want the destination %d", got.ProfileID, dest.ID)
	}
	if got.ProfileExpiresAt != nil || got.ProfileAfterExpiry != nil || got.RevokeKeyAtExpiry {
		t.Errorf("deadline not cleared: %v / %v / %v",
			got.ProfileExpiresAt, got.ProfileAfterExpiry, got.RevokeKeyAtExpiry)
	}

	// Idempotence: a second run must find nothing to do.
	due, err := s.UsersWithPassedDeadline(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("got %d users still due after applying, want 0", len(due))
	}
}

// A null destination means the default profile, which is expressed as a null
// profile_id — the user falls back to the default the same way a new user does.
func TestApplyProfileExpiryToDefaultClearsTheProfile(t *testing.T) {
	s := migratedStore(t, "pexp5")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-d", "d@uni-osnabrueck.de", "D")
	if err != nil {
		t.Fatal(err)
	}
	p := &Profile{Name: "temporary"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := s.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	if err := s.ApplyProfileExpiry(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID != nil {
		t.Errorf("ProfileID = %v, want nil so the default profile applies", got.ProfileID)
	}
}

// Deleting a profile that someone was due to land on must not leave a dangling
// reference: those users fall back to the default instead.
func TestDeleteProfileClearsPendingDestinations(t *testing.T) {
	s := migratedStore(t, "pexp6")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-e", "e@uni-osnabrueck.de", "E")
	if err != nil {
		t.Fatal(err)
	}
	dest := &Profile{Name: "doomed"}
	if err := s.CreateProfile(ctx, dest); err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := s.SetUserProfileUntil(ctx, u.ID, nil, &tomorrow, &dest.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProfile(ctx, dest.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileAfterExpiry != nil {
		t.Errorf("ProfileAfterExpiry = %v, want nil after the profile was deleted", got.ProfileAfterExpiry)
	}
	if got.ProfileExpiresAt == nil {
		t.Error("the deadline itself was cleared; only the destination should have been")
	}
}
