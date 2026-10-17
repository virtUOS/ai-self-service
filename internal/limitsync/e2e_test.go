//go:build e2e

package limitsync

// End-to-end test of the sync against a real gateway, like those in
// internal/litellm, which explains the setup: by default the one in
// dev/docker-compose.yml, or the one LITELLM_BASE_URL and LITELLM_MASTER_KEY
// point at.

import (
	"context"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/e2etest"
	"github.com/virtuos/keyosk/internal/keyprovider"
	"github.com/virtuos/keyosk/internal/litellm"
)

// A key deleted in the gateway behind Keyosk's back is marked by the sync,
// kept through the grace period, and removed by the first run after it
// (issue #108). The fake gateway in the unit tests covers each step; this
// checks they fit together against LiteLLM, with the reference Keyosk stores.
func TestE2EKeyDeletedInTheGatewayIsMarkedThenRemoved(t *testing.T) {
	gw := litellm.NewProvider(litellm.NewClient(e2etest.BaseURL(), e2etest.MasterKey()))
	f := newFixture(t)
	ctx := context.Background()
	const grace = 2 * time.Second

	sub := e2etest.Owner(t)
	u, err := f.store.GetOrCreateUser(ctx, sub, sub+"@uni-osnabrueck.de", sub)
	if err != nil {
		t.Fatal(err)
	}
	k, err := gw.CreateKey(ctx, keyprovider.KeyRequest{
		Alias: sub, User: keyprovider.User{ID: sub}, ExpiresAt: time.Now().AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	t.Cleanup(func() { _ = gw.DeleteKey(context.Background(), k.Ref) })
	if err := f.store.ReplaceAPIKey(ctx, &database.APIKey{UserID: u.ID, KeyRef: k.Ref,
		KeyPrefix: k.Secret[:8], ExpiresAt: time.Now().AddDate(0, 0, 1)}); err != nil {
		t.Fatal(err)
	}
	s := New(f.store, gw, plainUser, 1).WithMissingGrace(grace)

	// The stored reference reaches the key: the first push succeeds.
	if res, err := s.Run(ctx); err != nil || res.Synced != 1 {
		t.Fatalf("first run = %+v, %v; want the key synced", res, err)
	}

	if err := gw.DeleteKey(ctx, k.Ref); err != nil {
		t.Fatalf("delete key in the gateway: %v", err)
	}
	f.setTPM(t, 1234) // makes the key out of date, so the next run pushes it

	if res, err := s.Run(ctx); err != nil || res.Marked != 1 || res.Removed != 0 {
		t.Fatalf("run after the deletion = %+v, %v; want the key marked", res, err)
	}
	if k := f.keyOf(t, sub); k == nil || k.MissingSince == nil {
		t.Fatalf("key = %+v, want it kept and marked", k)
	}

	time.Sleep(grace + 500*time.Millisecond)
	if res, err := s.Run(ctx); err != nil || res.Removed != 1 {
		t.Fatalf("run after the grace period = %+v, %v; want the key removed", res, err)
	}
	if k := f.keyOf(t, sub); k != nil {
		t.Errorf("key = %+v, want it removed", k)
	}
	if n := auditCount(t, f, database.AuditKeyMissing); n != 1 {
		t.Errorf("key.missing audit entries = %d, want 1", n)
	}
	if n := auditCount(t, f, database.AuditKeyGone); n != 1 {
		t.Errorf("key.gone audit entries = %d, want 1", n)
	}
}
