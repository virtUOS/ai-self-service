package handlers

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/web"
)

// Templates are parsed at construction time in production; these tests catch
// field/name drift between the handler structs and the HTML.
func TestDashboardTemplateRenders(t *testing.T) {
	tmpl := template.Must(template.ParseFS(web.TemplateFS, "templates/dashboard.html"))
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, dashboardData{
		User:            &database.User{Name: "Test", Email: "t@example.com"},
		APIKey:          &database.APIKey{KeyPrefix: "sk-abc123", ExpiresAt: time.Now().Add(24 * time.Hour)},
		NewKey:          "sk-brand-new",
		APIBaseURL:      "https://litellm.example.com/v1",
		KeyDurationDays: 90,
		CSRFToken:       "TOK123",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// With a key present: logout + extend + regenerate + delete.
	if n := strings.Count(out, `name="csrf_token" value="TOK123"`); n != 4 {
		t.Errorf("csrf fields rendered = %d, want 4", n)
	}
	if !strings.Contains(out, "sk-brand-new") {
		t.Error("new key not rendered")
	}
	// The API base must be the gateway, not the portal: a client pointed at the
	// portal gets 404s because it serves HTML, not the OpenAI API.
	if !strings.Contains(out, "https://litellm.example.com/v1") {
		t.Error("dashboard does not show the gateway API base URL")
	}
	// Every form must carry a token — none may post unprotected.
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields — a form would post unprotected", forms, toks)
	}
}

// The no-key branch renders a different form set; it must be covered too.
func TestDashboardTemplateNoKeyBranch(t *testing.T) {
	tmpl := template.Must(template.ParseFS(web.TemplateFS, "templates/dashboard.html"))
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, dashboardData{
		User:      &database.User{Name: "Test", Email: "t@example.com"},
		CSRFToken: "TOK789",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// logout + generate
	if n := strings.Count(out, `name="csrf_token" value="TOK789"`); n != 2 {
		t.Errorf("csrf fields rendered = %d, want 2", n)
	}
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields", forms, toks)
	}
}

func TestAdminTemplateRenders(t *testing.T) {
	tmpl := parseAdminTemplate()
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, adminData{
		Profiles: []database.Profile{
			{ID: 1, Name: "default", IsDefault: true},
			{ID: 2, Name: "students", KeyDurationDays: 30, QuotaTokens: 1_500_000, QuotaPeriod: "24h"},
		},
		Users:     []userRow{{User: database.User{ID: 2, Name: "U", Email: "u@x.de"}}},
		CSRFToken: "TOK456",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// the default profile hides its delete form; the second profile shows one
	if n := strings.Count(out, `name="csrf_token" value="TOK456"`); n != 3 {
		t.Errorf("csrf fields rendered = %d, want 3", n)
	}
	// Quotas must render in tokens, formatted, not as raw spend.
	if !strings.Contains(out, "1.5M / 24h") {
		t.Error("token quota not rendered in admin table")
	}
	if !strings.Contains(out, "30d") {
		t.Error("per-profile expiry not rendered in admin table")
	}
	if forms, toks := strings.Count(out, "<form"), strings.Count(out, `name="csrf_token"`); forms != toks {
		t.Errorf("%d forms but %d csrf fields", forms, toks)
	}
}
