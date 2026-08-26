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
	// Each name must be copyable verbatim: they are long and easy to mistype.
	for _, m := range withModels.Models {
		if !strings.Contains(out, `data-model="`+m+`"`) {
			t.Errorf("model %q is not click-to-copy", m)
		}
	}
	if n := strings.Count(out, "copyModel(this)"); n != len(withModels.Models) {
		t.Errorf("%d copy handlers for %d models", n, len(withModels.Models))
	}

	buf.Reset()
	if err := parseDashboardTemplate().Execute(&buf, base); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(buf.String(), "model-list") {
		t.Error("empty model list should omit the row entirely")
	}
}

// Copying must be confirmed in words. A colour change alone reads as a hover
// effect, so the chip says "Kopiert!"/"Copied!" like the other copy buttons.
func TestModelCopyConfirmsInWords(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.DE, i18n.EN} {
		var buf bytes.Buffer
		if err := parseDashboardTemplate().Execute(&buf, dashboardData{
			Lang:      lang,
			User:      &database.User{Name: "T", Email: "t@example.com"},
			APIKey:    &database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(24 * time.Hour)},
			Models:    []string{"gpt-4o"},
			CSRFToken: "TOK",
		}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		// The chip copies a curl example, so it confirms with its own wording
		// rather than the generic "Copied!" the other copy buttons use.
		want := i18n.T(lang, "dash.models.copied")
		if !strings.Contains(out, want) {
			t.Errorf("%s: copy confirmation %q not available to the script", lang, want)
		}
		if !strings.Contains(out, "code.textContent = CURL_COPIED") {
			t.Errorf("%s: chip does not swap its label on copy", lang)
		}
	}
}

// Clicking a model copies a complete curl example for it, not just the name:
// knowing the model is only half of what someone needs to make a first call.
func TestModelChipCopiesACurlExample(t *testing.T) {
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:       i18n.EN,
		User:       &database.User{Name: "T", Email: "t@example.com"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(24 * time.Hour)},
		APIBaseURL: "https://gateway.example/v1",
		Models:     []string{"gpt-4o"},
		CSRFToken:  "TOK",
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// The example is built in the page, so the base URL has to reach the script.
	if !strings.Contains(out, "https://gateway.example/v1") {
		t.Error("base URL not available to the curl example")
	}
	for _, want := range []string{"chat/completions", "Authorization: Bearer", "curlExample("} {
		if !strings.Contains(out, want) {
			t.Errorf("curl example is missing %q", want)
		}
	}
}

// The example must reference the key by environment variable. The page only
// ever shows a prefix, and a command carrying a live secret would land in the
// user's shell history.
func TestCurlExampleDoesNotEmbedTheKey(t *testing.T) {
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang:       i18n.EN,
		User:       &database.User{Name: "T", Email: "t@example.com"},
		APIKey:     &database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(24 * time.Hour)},
		APIBaseURL: "https://gateway.example/v1",
		NewKey:     "sk-supersecretvalue",
		Models:     []string{"gpt-4o"},
		CSRFToken:  "TOK",
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, "$OPENAI_API_KEY") {
		t.Error("curl example does not read the key from the environment")
	}
	// The freshly issued key is shown once in its own box; it must not also be
	// baked into a command the user pastes into a terminal.
	if strings.Count(out, "sk-supersecretvalue") != 1 {
		t.Error("the secret appears outside its own box, likely in the curl example")
	}
}
