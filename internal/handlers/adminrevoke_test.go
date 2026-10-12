package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// newRevokeTestAdmin is an Admin backed by a fake gateway, and a user other
// than the acting admin who holds a key in it.
func newRevokeTestAdmin(t *testing.T, name string) (*Admin, *database.Store, string, *keyprovider.Fake, *database.User) {
	t.Helper()
	a, store, token := newGrantTestAdmin(t, name)
	fake := keyprovider.NewFake()
	a.keys = fake

	ctx := context.Background()
	user, err := store.GetOrCreateUser(ctx, "sub-holder", "holder@uni-osnabrueck.de", "Holder")
	if err != nil {
		t.Fatal(err)
	}
	giveKey(t, store, fake, user, "holder")
	return a, store, token, fake, user
}

// giveKey creates a key in the fake gateway and records it for user.
func giveKey(t *testing.T, store *database.Store, fake *keyprovider.Fake, user *database.User, alias string) *database.APIKey {
	t.Helper()
	ctx := context.Background()
	res, err := fake.CreateKey(ctx, keyprovider.KeyRequest{Alias: alias})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAPIKey(ctx, &database.APIKey{
		UserID:    user.ID,
		KeyRef:    res.Ref,
		KeyPrefix: res.Secret[:10],
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	k, err := store.GetAPIKeyByUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Revoking deletes the key in the gateway and the portal's record of it,
// audits who revoked whose key, and leaves everyone else's key alone.
func TestRevokeUserKeyRevokesAndAudits(t *testing.T) {
	a, store, token, fake, user := newRevokeTestAdmin(t, "revoke1")
	ctx := context.Background()
	key, _ := store.GetAPIKeyByUser(ctx, user.ID)

	bystander, err := store.GetOrCreateUser(ctx, "sub-bystander", "bystander@uni-osnabrueck.de", "Bystander")
	if err != nil {
		t.Fatal(err)
	}
	other := giveKey(t, store, fake, bystander, "bystander")

	rec := postAdminForm(t, a.RevokeUserKey, token, "/admin/users/1/key/revoke", user.ID, nil)
	if got := flashOf(t, rec.Header().Get("Location")); got != "Key revoked" {
		t.Errorf("flash = %q, want %q", got, "Key revoked")
	}

	if k, _ := store.GetAPIKeyByUser(ctx, user.ID); k != nil {
		t.Error("key row still present after revoke")
	}
	if len(fake.Deleted) != 1 || fake.Deleted[0] != key.KeyRef {
		t.Errorf("gateway deletes = %v, want only %s", fake.Deleted, key.KeyRef)
	}
	if k, _ := store.GetAPIKeyByUser(ctx, bystander.ID); k == nil || k.KeyRef != other.KeyRef {
		t.Error("revoking one user's key touched another user's")
	}

	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("audit = %+v, want one event", events)
	}
	e := events[0]
	if e.Action != database.AuditKeyRevoked || e.ActorEmail != actingAdminEmail ||
		e.SubjectEmail != user.Email || e.SubjectID == nil || *e.SubjectID != user.ID {
		t.Errorf("audit event = %+v, want %s of %s's key by %s",
			e, database.AuditKeyRevoked, user.Email, actingAdminEmail)
	}
}

// A failed gateway delete leaves the key working, so the row must stay to keep
// it revocable, and the admin must not be told it is gone.
func TestRevokeUserKeyKeepsRowWhenUpstreamFails(t *testing.T) {
	a, store, token, fake, user := newRevokeTestAdmin(t, "revoke2")
	ctx := context.Background()
	key, _ := store.GetAPIKeyByUser(ctx, user.ID)

	fake.DeleteErr = errors.New("gateway down")
	rec := postAdminForm(t, a.RevokeUserKey, token, "/admin/users/1/key/revoke", user.ID, nil)
	if got := flashOf(t, rec.Header().Get("Location")); got != "Failed to revoke key upstream" {
		t.Errorf("flash = %q, want the upstream failure", got)
	}
	if k, _ := store.GetAPIKeyByUser(ctx, user.ID); k == nil || k.KeyRef != key.KeyRef {
		t.Fatal("key row lost although the key is still live upstream")
	}
	if events, _ := store.ListAuditEvents(ctx, 10); len(events) != 0 {
		t.Errorf("audit = %+v, want nothing for a revoke that did not happen", events)
	}

	// Once the gateway recovers, the same key can still be revoked.
	fake.DeleteErr = nil
	postAdminForm(t, a.RevokeUserKey, token, "/admin/users/1/key/revoke", user.ID, nil)
	if fake.LiveCount() != 0 {
		t.Errorf("gateway still holds %d keys after the retry", fake.LiveCount())
	}
	if k, _ := store.GetAPIKeyByUser(ctx, user.ID); k != nil {
		t.Error("key row still present after the retry")
	}
}

// A user without a key, or no such user, is reported as such, and nothing is
// deleted or audited.
func TestRevokeUserKeyWithoutAKey(t *testing.T) {
	a, store, token, fake, _ := newRevokeTestAdmin(t, "revoke3")
	ctx := context.Background()
	keyless, err := store.GetOrCreateUser(ctx, "sub-keyless", "keyless@uni-osnabrueck.de", "Keyless")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		id   int64
		want string
	}{
		{keyless.ID, "User has no key"},
		{9999, "User not found"},
	} {
		rec := postAdminForm(t, a.RevokeUserKey, token, "/admin/users/1/key/revoke", tc.id, nil)
		if got := flashOf(t, rec.Header().Get("Location")); got != tc.want {
			t.Errorf("user %d: flash = %q, want %q", tc.id, got, tc.want)
		}
	}
	if len(fake.Deleted) != 0 {
		t.Errorf("gateway deletes = %v, want none", fake.Deleted)
	}
	if events, _ := store.ListAuditEvents(ctx, 10); len(events) != 0 {
		t.Errorf("audit = %+v, want nothing", events)
	}
}
