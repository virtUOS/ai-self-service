package database

import (
	"context"
	"testing"
	"time"
)

// limitSyncFixture is a store with the seeded default profile and one user
// holding a key that is up to date with it.
type limitSyncFixture struct {
	s    *Store
	ctx  context.Context
	def  *Profile
	user *User
	key  *APIKey
}

func newLimitSyncFixture(t *testing.T, name string) *limitSyncFixture {
	t.Helper()
	s := migratedStore(t, name)
	ctx := context.Background()
	if err := s.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	def, err := s.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := &limitSyncFixture{s: s, ctx: ctx, def: def}
	f.user, f.key = f.addUserWithKey(t, "sub-a", "a@uni-osnabrueck.de")
	f.markSynced(t, f.key, def)
	return f
}

func (f *limitSyncFixture) addUserWithKey(t *testing.T, sub, email string) (*User, *APIKey) {
	t.Helper()
	u, err := f.s.GetOrCreateUser(f.ctx, sub, email, sub)
	if err != nil {
		t.Fatal(err)
	}
	k := &APIKey{UserID: u.ID, LiteLLMKey: "sk-" + sub, KeyPrefix: "sk-" + sub,
		ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := f.s.ReplaceAPIKey(f.ctx, k); err != nil {
		t.Fatal(err)
	}
	return u, k
}

// markSynced records the profile's current revision on the key, as a
// successful push would.
func (f *limitSyncFixture) markSynced(t *testing.T, k *APIKey, p *Profile) {
	t.Helper()
	cur, err := f.s.GetProfile(f.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.MarkLimitsSynced(f.ctx, k.ID, cur.ID, cur.LimitsRev); err != nil {
		t.Fatal(err)
	}
}

func (f *limitSyncFixture) pending(t *testing.T) []LimitSyncTarget {
	t.Helper()
	rows, err := f.s.PendingLimitSyncs(f.ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f *limitSyncFixture) createProfile(t *testing.T, name string, tpm int64) *Profile {
	t.Helper()
	p := &Profile{Name: name, TPMLimit: &tpm}
	if err := f.s.CreateProfile(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A key nobody has pushed limits to is out of date, and a recorded push of the
// current revision brings it up to date.
func TestNewKeyIsPendingUntilSynced(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-new")
	if got := f.pending(t); len(got) != 0 {
		t.Fatalf("pending = %d, want 0 for a synced key", len(got))
	}

	_, k := f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")
	got := f.pending(t)
	if len(got) != 1 || got[0].KeyID != k.ID {
		t.Fatalf("pending = %+v, want only the new key", got)
	}
	if got[0].ProfileID == nil || *got[0].ProfileID != f.def.ID {
		t.Errorf("effective profile = %v, want the default %d", got[0].ProfileID, f.def.ID)
	}
	if got[0].OIDCSub != "sub-b" || got[0].Email != "b@uni-osnabrueck.de" {
		t.Errorf("owner = %q/%q, want sub-b and its email", got[0].OIDCSub, got[0].Email)
	}

	f.markSynced(t, k, f.def)
	if got := f.pending(t); len(got) != 0 {
		t.Errorf("pending = %d after the push was recorded, want 0", len(got))
	}
}

// Changing what a key enforces marks it; changing only how the profile is
// described does not, so a typo fix does not push to every key.
func TestProfileEditMarksKeysOnlyForLimitChanges(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-edit")
	p := f.def

	p.Description = "reworded"
	if err := f.s.UpdateProfile(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 0 {
		t.Fatalf("pending = %d after a description change, want 0", len(got))
	}

	tpm := int64(1000)
	p.TPMLimit = &tpm
	if err := f.s.UpdateProfile(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 1 {
		t.Fatalf("pending = %d after a TPM change, want 1", len(got))
	}

	f.markSynced(t, f.key, p)
	p.Models = []string{"gpt-x"}
	if err := f.s.UpdateProfile(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 1 {
		t.Errorf("pending = %d after a model change, want 1", len(got))
	}
}

func TestQuotaChangeMarksKeys(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-quota")
	windows := []ProfileQuota{{Budget: 5, Period: "24h"}, {Budget: 20, Period: "7d"}}
	if err := f.s.SetProfileQuotas(f.ctx, f.def.ID, windows); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 1 {
		t.Fatalf("pending = %d after adding windows, want 1", len(got))
	}
	f.markSynced(t, f.key, f.def)

	// The same windows in a different order enforce the same thing.
	same := []ProfileQuota{{Budget: 20, Period: "7d"}, {Budget: 5, Period: "24h"}}
	if err := f.s.SetProfileQuotas(f.ctx, f.def.ID, same); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 0 {
		t.Fatalf("pending = %d after re-saving the same windows, want 0", len(got))
	}

	if err := f.s.SetProfileQuotas(f.ctx, f.def.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 1 {
		t.Errorf("pending = %d after removing the windows, want 1", len(got))
	}
}

func TestReassigningAUserMarksTheirKey(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-assign")
	other := f.createProfile(t, "other", 10)

	if err := f.s.SetUserProfileUntil(f.ctx, f.user.ID, &other.ID, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	got := f.pending(t)
	if len(got) != 1 || got[0].ProfileID == nil || *got[0].ProfileID != other.ID {
		t.Fatalf("pending = %+v, want the key, now on profile %d", got, other.ID)
	}
}

// Users without a profile of their own follow the default, so making another
// profile the default marks their keys, and leaves users with their own
// profile alone.
func TestChangingTheDefaultMarksUnassignedUsers(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-default")
	own := f.createProfile(t, "own", 10)
	assigned, k := f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")
	if err := f.s.SetUserProfileUntil(f.ctx, assigned.ID, &own.ID, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	f.markSynced(t, k, own)

	newDefault := f.createProfile(t, "new default", 20)
	newDefault.IsDefault = true
	if err := f.s.UpdateProfile(f.ctx, newDefault); err != nil {
		t.Fatal(err)
	}

	got := f.pending(t)
	if len(got) != 1 || got[0].KeyID != f.key.ID {
		t.Fatalf("pending = %+v, want only the unassigned user's key", got)
	}
	if *got[0].ProfileID != newDefault.ID {
		t.Errorf("effective profile = %d, want the new default %d", *got[0].ProfileID, newDefault.ID)
	}
}

// With no default profile an unassigned user has no limits to push. The key
// must still be listed, so the sync can report it, with no profile.
func TestKeyWithoutAnyProfileIsPending(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-noprofile")
	if _, err := f.s.db.NewUpdate().Model((*Profile)(nil)).
		Set("is_default = ?", false).Where("1 = 1").Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	got := f.pending(t)
	if len(got) != 1 || got[0].ProfileID != nil {
		t.Fatalf("pending = %+v, want the key with no profile", got)
	}
}

// A key that failed during the current run is left for the next one.
func TestFailedKeyIsSkippedUntilTheNextRun(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-failed")
	_, k := f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")

	runStart := time.Now()
	failedAt := runStart.Add(250 * time.Millisecond)
	if err := f.s.MarkLimitSyncFailed(f.ctx, k.ID, "gateway down", failedAt); err != nil {
		t.Fatal(err)
	}

	got, err := f.s.PendingLimitSyncs(f.ctx, runStart, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("same run: pending = %+v, want the failed key skipped", got)
	}

	got, err = f.s.PendingLimitSyncs(f.ctx, failedAt.Add(time.Millisecond), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SyncError != "gateway down" {
		t.Fatalf("next run: pending = %+v, want the failed key with its error", got)
	}

	// A successful push clears the failure.
	f.markSynced(t, k, f.def)
	got, err = f.s.PendingLimitSyncs(f.ctx, failedAt.Add(time.Millisecond), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v after a successful push, want none", got)
	}
}

// Keys that never failed go first, so a key that keeps failing does not hold
// up the rest.
func TestPendingListsNeverFailedKeysFirst(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-order")
	if err := f.s.MarkLimitSyncFailed(f.ctx, f.key.ID, "boom", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.MarkAllLimitsPending(f.ctx); err != nil {
		t.Fatal(err)
	}
	_, k := f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")

	got, err := f.s.PendingLimitSyncs(f.ctx, time.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].KeyID != k.ID {
		t.Errorf("first pending = %+v, want the never-failed key %d", got, k.ID)
	}
}

func TestMarkAllLimitsPending(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-all")
	_, k := f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")
	f.markSynced(t, k, f.def)

	n, err := f.s.MarkAllLimitsPending(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("marked %d keys, want 2", n)
	}
	if got := f.pending(t); len(got) != 2 {
		t.Errorf("pending = %d, want 2", len(got))
	}
}

func TestLimitSyncStatus(t *testing.T) {
	f := newLimitSyncFixture(t, "ls-status")
	other := f.createProfile(t, "other", 10)

	st, err := f.s.GetLimitSyncStatus(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 0 || st.Failed != 0 || len(st.RecentFailures) != 0 {
		t.Fatalf("status = %+v, want nothing pending", st)
	}

	_, kb := f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")
	uc, _ := f.addUserWithKey(t, "sub-c", "c@uni-osnabrueck.de")
	if err := f.s.SetUserProfileUntil(f.ctx, uc.ID, &other.ID, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := f.s.MarkLimitSyncFailed(f.ctx, kb.ID, "gateway down", time.Now()); err != nil {
		t.Fatal(err)
	}

	st, err = f.s.GetLimitSyncStatus(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 2 || st.Failed != 1 {
		t.Errorf("pending/failed = %d/%d, want 2/1", st.Pending, st.Failed)
	}
	if st.PendingByProfile[f.def.ID] != 1 || st.PendingByProfile[other.ID] != 1 {
		t.Errorf("by profile = %v, want one each on %d and %d", st.PendingByProfile, f.def.ID, other.ID)
	}
	if len(st.RecentFailures) != 1 || st.RecentFailures[0].Email != "b@uni-osnabrueck.de" ||
		st.RecentFailures[0].Error != "gateway down" {
		t.Errorf("failures = %+v, want b's gateway error", st.RecentFailures)
	}
}
