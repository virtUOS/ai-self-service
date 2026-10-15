package database

import (
	"testing"
	"time"
)

// auditActions returns the audit actions recorded, newest first.
func (f *limitSyncFixture) auditActions(t *testing.T) []AuditEvent {
	t.Helper()
	events, err := f.s.ListAuditEvents(f.ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// markMissing marks f.key as missing and fails the test if that did nothing.
func (f *limitSyncFixture) markMissing(t *testing.T) {
	t.Helper()
	newly, err := f.s.MarkKeyMissing(f.ctx, f.key.ID, f.user.ID, f.user.Email, "key sk-sub-a")
	if err != nil || !newly {
		t.Fatalf("mark: newly = %v, err = %v; want it marked", newly, err)
	}
}

func (f *limitSyncFixture) storedKey(t *testing.T) *APIKey {
	t.Helper()
	k, err := f.s.GetAPIKeyByUser(f.ctx, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A key the gateway no longer has is marked, not dropped, and the first
// sighting is recorded once however many requests notice it. The second
// sighting must not move the time that starts the grace period.
func TestMarkKeyMissing(t *testing.T) {
	f := newLimitSyncFixture(t, "km-mark")

	f.markMissing(t)
	k := f.storedKey(t)
	if k == nil || k.MissingSince == nil {
		t.Fatalf("key = %+v, want it kept and marked", k)
	}
	first := *k.MissingSince

	newly, err := f.s.MarkKeyMissing(f.ctx, f.key.ID, f.user.ID, f.user.Email, "key sk-sub-a")
	if err != nil || newly {
		t.Errorf("second mark: newly = %v, err = %v; want nothing to do", newly, err)
	}
	if got := f.storedKey(t).MissingSince; got == nil || !got.Equal(first) {
		t.Errorf("missing_since = %v after a second sighting, want the first, %v", got, first)
	}

	var missing []AuditEvent
	for _, e := range f.auditActions(t) {
		if e.Action == AuditKeyMissing {
			missing = append(missing, e)
		}
	}
	if len(missing) != 1 || missing[0].ActorEmail != "system" ||
		missing[0].SubjectEmail != f.user.Email || missing[0].Detail != "key sk-sub-a" {
		t.Errorf("audit = %+v, want one key.missing entry by system for a's key", missing)
	}
}

// A marked key leaves the sync's work: the gateway does not have it, so a push
// could only fail. It does not count as out of date either.
func TestMarkedKeyIsNotPending(t *testing.T) {
	f := newLimitSyncFixture(t, "km-pending")
	other := f.createProfile(t, "other", 5)
	if err := f.s.SetUserProfile(f.ctx, f.user.ID, &other.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(t); len(got) != 1 {
		t.Fatalf("pending = %+v, want the key out of date", got)
	}

	f.markMissing(t)
	if got := f.pending(t); len(got) != 0 {
		t.Errorf("pending = %+v after marking, want nothing", got)
	}
	st, err := f.s.GetLimitSyncStatus(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 0 {
		t.Errorf("status pending = %d, want 0", st.Pending)
	}
}

// A key that returns is unmarked, recorded, and out of date, so what changed
// while it was gone reaches it.
func TestClearKeyMissing(t *testing.T) {
	f := newLimitSyncFixture(t, "km-clear")
	f.markMissing(t)
	if got := f.pending(t); len(got) != 0 {
		t.Fatalf("pending = %+v while marked, want nothing", got)
	}

	cleared, err := f.s.ClearKeyMissing(f.ctx, f.key.ID, f.user.ID, f.user.Email, "back")
	if err != nil || !cleared {
		t.Fatalf("clear: cleared = %v, err = %v; want it cleared", cleared, err)
	}
	if k := f.storedKey(t); k == nil || k.MissingSince != nil {
		t.Errorf("key = %+v, want it kept and unmarked", k)
	}
	if got := f.pending(t); len(got) != 1 {
		t.Errorf("pending = %+v after clearing, want the key out of date", got)
	}
	if cleared, err := f.s.ClearKeyMissing(f.ctx, f.key.ID, f.user.ID, f.user.Email, "back"); err != nil || cleared {
		t.Errorf("clearing an unmarked key: cleared = %v, err = %v; want nothing to do", cleared, err)
	}

	found := 0
	for _, e := range f.auditActions(t) {
		if e.Action == AuditKeyFound && e.ActorEmail == "system" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("key.found audit entries = %d, want 1", found)
	}
}

// A marked key is removed only once its first sighting is older than the cutoff,
// with an audit entry saying so, and only once.
func TestRemoveMissingKeyWaitsForTheGracePeriod(t *testing.T) {
	f := newLimitSyncFixture(t, "km-remove")

	// Not marked: nothing to remove, whatever the cutoff.
	if removed, err := f.s.RemoveMissingKey(f.ctx, f.key.ID, f.user.ID, f.user.Email, "x", time.Now().Add(time.Hour)); err != nil || removed {
		t.Fatalf("removing an unmarked key: removed = %v, err = %v; want it kept", removed, err)
	}

	f.markMissing(t)
	if removed, err := f.s.RemoveMissingKey(f.ctx, f.key.ID, f.user.ID, f.user.Email, "x", time.Now().Add(-time.Hour)); err != nil || removed {
		t.Fatalf("within the grace period: removed = %v, err = %v; want it kept", removed, err)
	}
	if f.storedKey(t) == nil {
		t.Fatal("key removed within the grace period")
	}

	removed, err := f.s.RemoveMissingKey(f.ctx, f.key.ID, f.user.ID, f.user.Email, "stayed missing", time.Now().Add(time.Hour))
	if err != nil || !removed {
		t.Fatalf("past the grace period: removed = %v, err = %v; want the row removed", removed, err)
	}
	if k := f.storedKey(t); k != nil {
		t.Errorf("key after removal = %+v, want none", k)
	}
	if removed, err := f.s.RemoveMissingKey(f.ctx, f.key.ID, f.user.ID, f.user.Email, "x", time.Now().Add(time.Hour)); err != nil || removed {
		t.Errorf("second removal: removed = %v, err = %v; want nothing to do", removed, err)
	}

	var gone []AuditEvent
	for _, e := range f.auditActions(t) {
		if e.Action == AuditKeyGone {
			gone = append(gone, e)
		}
	}
	if len(gone) != 1 || gone[0].ActorEmail != "system" || gone[0].Detail != "stayed missing" {
		t.Errorf("audit = %+v, want one key.gone entry by system", gone)
	}
}

func TestListAndCountMissingKeys(t *testing.T) {
	f := newLimitSyncFixture(t, "km-list")
	if n, err := f.s.CountMissingKeys(f.ctx); err != nil || n != 0 {
		t.Fatalf("count = %d, err = %v; want 0", n, err)
	}
	f.addUserWithKey(t, "sub-b", "b@uni-osnabrueck.de")
	f.markMissing(t)

	n, err := f.s.CountMissingKeys(f.ctx)
	if err != nil || n != 1 {
		t.Errorf("count = %d, err = %v; want 1", n, err)
	}
	rows, err := f.s.ListMissingKeys(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].KeyID != f.key.ID || rows[0].KeyRef != "sk-sub-a" ||
		rows[0].Email != f.user.Email || rows[0].UserID != f.user.ID || rows[0].MissingSince.IsZero() {
		t.Errorf("missing keys = %+v, want only a's key", rows)
	}
}

// A key the gateway does not have is not worth a reminder to extend it. The
// notice is not recorded, so it still goes out if the key comes back.
func TestMarkedKeyGetsNoExpiryReminder(t *testing.T) {
	f := newLimitSyncFixture(t, "km-reminder")
	soon := time.Now().Add(48 * time.Hour)
	if err := f.s.UpdateAPIKeyExpiry(f.ctx, f.key.ID, soon); err != nil {
		t.Fatal(err)
	}
	if rows, err := f.s.KeysExpiringWithin(f.ctx, 7); err != nil || len(rows) != 1 {
		t.Fatalf("expiring = %+v, err = %v; want the key", rows, err)
	}

	f.markMissing(t)
	if rows, err := f.s.KeysExpiringWithin(f.ctx, 7); err != nil || len(rows) != 0 {
		t.Errorf("expiring while marked = %+v, err = %v; want nothing", rows, err)
	}
	if _, err := f.s.ClearKeyMissing(f.ctx, f.key.ID, f.user.ID, f.user.Email, "back"); err != nil {
		t.Fatal(err)
	}
	if rows, err := f.s.KeysExpiringWithin(f.ctx, 7); err != nil || len(rows) != 1 {
		t.Errorf("expiring after the key returned = %+v, err = %v; want the key again", rows, err)
	}
}
