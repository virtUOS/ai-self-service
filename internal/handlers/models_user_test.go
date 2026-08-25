package handlers

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// A user must see the models their key will actually accept: the profile's
// list when it restricts, otherwise everything the gateway serves. Showing
// the full list to a restricted profile advertises models whose requests are
// rejected. See issue #8.
func TestUserModelsRespectProfileRestriction(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.AvailableModels = []string{"gpt-4o", "llama-3", "mistral"}
	u := &UI{models: newModelCache(fake)}
	ctx := context.Background()

	restricted := &database.Profile{Models: []string{"llama-3"}}
	got := u.userModels(ctx, restricted)
	if len(got) != 1 || got[0] != "llama-3" {
		t.Errorf("restricted profile = %v, want [llama-3]", got)
	}

	unrestricted := &database.Profile{}
	got = u.userModels(ctx, unrestricted)
	if len(got) != 3 {
		t.Errorf("unrestricted profile = %v, want all gateway models", got)
	}

	if got = u.userModels(ctx, nil); len(got) != 3 {
		t.Errorf("no profile = %v, want all gateway models", got)
	}
}

// The gateway being unreachable must not render an empty models row.
func TestUserModelsEmptyWhenGatewaySilent(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.ModelsErr = context.DeadlineExceeded
	u := &UI{models: newModelCache(fake)}

	if got := u.userModels(context.Background(), nil); len(got) != 0 {
		t.Errorf("unreachable gateway = %v, want empty", got)
	}
}

// A provider that cannot enumerate models must not break the dashboard.
func TestUserModelsNilListerIsSafe(t *testing.T) {
	u := &UI{models: newModelCache(nil)}
	if got := u.userModels(context.Background(), nil); len(got) != 0 {
		t.Errorf("nil lister = %v, want empty", got)
	}
}

// The dashboard must render the model names, and omit the row entirely when
// there are none rather than showing an empty list.
func TestDashboardRendersModels(t *testing.T) {
	base := dashboardData{
		Lang:       i18n.DE,
		User:       &database.User{Name: "T", Email: "t@example.com"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(24 * time.Hour)},
		APIBaseURL: "https://gw.example.com/v1",
		CSRFToken:  "TOK",
	}

	withModels := base
	withModels.Models = []string{"gpt-4o", "llama-3"}
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, withModels); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, m := range withModels.Models {
		if !strings.Contains(out, m) {
			t.Errorf("model %q not rendered", m)
		}
	}
	if !strings.Contains(out, "Verfügbare Modelle") {
		t.Error("models label not rendered in German")
	}

	buf.Reset()
	if err := parseDashboardTemplate().Execute(&buf, base); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(buf.String(), "model-list") {
		t.Error("empty model list should omit the row entirely")
	}
}
