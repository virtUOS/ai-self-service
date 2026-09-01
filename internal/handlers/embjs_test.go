package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
)

// The map is interpolated straight into a script. A nil map must still produce
// something a property lookup works on: `null[model]` throws and would break
// every curl example on the page, not just an embedding one.
func TestEmbeddingMapRendersAsUsableJS(t *testing.T) {
	render := func(m map[string]bool) string {
		d := dashboardData{
			Lang: i18n.EN, User: &database.User{Name: "T", Email: "t@e.com"},
			APIKey:     &database.APIKey{KeyPrefix: "sk", ExpiresAt: time.Now().Add(time.Hour)},
			APIBaseURL: "https://gw/v1", CSRFToken: "T",
			Models: []string{"m"}, EmbeddingModels: m,
		}
		var b bytes.Buffer
		if err := parseDashboardTemplate().Execute(&b, d); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(b.String(), "\n") {
			if strings.Contains(line, "const EMBEDDING_MODELS") {
				return strings.TrimSpace(line)
			}
		}
		t.Fatal("EMBEDDING_MODELS not found")
		return ""
	}

	// A bare `null` would throw on lookup, so the line must guard against it.
	// Checking for the guard rather than the absence of "null": Go renders a
	// nil map as null and the template falls back to {} on top of it.
	got := render(nil)
	if !strings.Contains(got, "|| {}") {
		t.Errorf("nil map rendered as %q with no fallback; a lookup on null throws", got)
	}
	if got := render(map[string]bool{"bge-m3": true}); !strings.Contains(got, "bge-m3") {
		t.Errorf("populated map rendered as %q", got)
	}
}
