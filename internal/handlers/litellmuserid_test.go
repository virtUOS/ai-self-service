package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// A portal with LITELLM_USER_ID_PREFIX binds keys to its own gateway users, so
// it never shares a person's allowance with another portal (issue #82).
func TestGenerateKeyUsesThePrefixedOwner(t *testing.T) {
	ui, fake, _, _ := newTestUI(t, "uid-create")
	ui.cfg.LiteLLMUserIDPrefix = "testing"

	post(t, ui, ui.GenerateKey, "/key/generate")
	if len(fake.Created) != 1 {
		t.Fatalf("created %d keys, want 1", len(fake.Created))
	}
	if got := fake.Created[0].User.ID; got != "testing:sub-1" {
		t.Errorf("owner id = %q, want testing:sub-1", got)
	}
	// The alias is a label for people reading LiteLLM's UI and keeps the
	// plain subject.
	if !strings.HasPrefix(fake.Created[0].Alias, "sub-1-") {
		t.Errorf("alias %q does not lead with the subject", fake.Created[0].Alias)
	}
}

// The dashboard reads the allowance from the same prefixed user, or it would
// report another portal's figures, or none.
func TestDashboardReadsThePrefixedOwnersQuota(t *testing.T) {
	ui, fake, _, _ := newTestUI(t, "uid-dash")
	ui.cfg.LiteLLMUserIDPrefix = "testing"
	post(t, ui, ui.GenerateKey, "/key/generate")
	fake.QuotaByOwner = map[string]keyprovider.Quota{
		"sub-1":         {Used: 0.9, Limit: 1},
		"testing:sub-1": {Used: 0.25, Limit: 1},
	}

	body := getPage(t, ui, ui.Dashboard, "/").Body.String()
	if !strings.Contains(body, "quota-fill") {
		t.Fatal("dashboard shows no quota; it did not read the prefixed owner")
	}
	if strings.Contains(body, "quota-full") {
		t.Error("dashboard shows the unprefixed user's nearly spent quota")
	}
}

// A new key labels its owner's gateway user with their name, and with their
// email only where LITELLM_USER_EMAIL allows it.
func TestGenerateKeyLabelsTheOwner(t *testing.T) {
	for _, withEmail := range []bool{false, true} {
		ui, fake, _, _ := newTestUI(t, fmt.Sprintf("uid-label-%v", withEmail))
		ui.cfg.LiteLLMUserEmail = withEmail

		post(t, ui, ui.GenerateKey, "/key/generate")
		if len(fake.Created) != 1 {
			t.Fatalf("created %d keys, want 1", len(fake.Created))
		}
		u := fake.Created[0].User
		wantEmail := ""
		if withEmail {
			wantEmail = "s@uni-osnabrueck.de"
		}
		if u.Name != "S" || u.Email != wantEmail {
			t.Errorf("email option %v: user = %+v, want name S and email %q", withEmail, u, wantEmail)
		}
	}
}

// The new key records what its owner was labelled with, so the limit sync
// does not push it again straight away.
func TestGenerateKeyIsNotPendingAfterwards(t *testing.T) {
	ui, _, store, _ := newTestUI(t, "uid-recorded")
	post(t, ui, ui.GenerateKey, "/key/generate")

	pending, err := store.PendingLimitSyncs(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want the new key up to date", pending)
	}
}
