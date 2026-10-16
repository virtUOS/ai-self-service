package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
)

func renderDashboard(t *testing.T, lang i18n.Lang) string {
	t.Helper()
	var buf bytes.Buffer
	err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang: lang,
		Path: "/",
		User: &database.User{Name: "T", Email: "t@uni.de"},
		APIKey: &database.APIKey{
			KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
		APIBaseURL: "https://litellm.example.com/v1",
		CSRFToken:  "T",
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The same page must render in either language.
func TestDashboardRendersBothLanguages(t *testing.T) {
	de := renderDashboard(t, i18n.DE)
	if !strings.Contains(de, "Ihr Konto") {
		t.Error("German dashboard missing translated heading")
	}
	if strings.Contains(de, "Your account") {
		t.Error("German dashboard still shows English")
	}

	en := renderDashboard(t, i18n.EN)
	if !strings.Contains(en, "Your account") {
		t.Error("English dashboard missing heading")
	}
	if strings.Contains(en, "Ihr Konto") {
		t.Error("English dashboard shows German")
	}
}

// The avatar shows one letter: upper-cased, a letter rather than leading
// punctuation, whole when it is not ASCII, and a placeholder for a name with
// none.
func TestInitial(t *testing.T) {
	for name, want := range map[string]string{
		"lkiesow":    "L",
		"Test Admin": "T",
		"önder":      "Ö",
		" _x":        "X",
		"":           "?",
		"--":         "?",
	} {
		if got := initial(name); got != want {
			t.Errorf("initial(%q) = %q, want %q", name, got, want)
		}
	}
}
