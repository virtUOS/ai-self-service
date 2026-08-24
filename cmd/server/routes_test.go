package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/virtuos/ai-self-service/internal/handlers"
)

// The real router needs OIDC discovery at startup, so this asserts the
// middleware and health handlers in isolation.
func TestSecurityHeadersApplied(t *testing.T) {
	r := chi.NewRouter()
	r.Use(handlers.SecurityHeaders(true, "https://login.uni-osnabrueck.de"))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q; got %q", directive, csp)
		}
	}
	// The sign-out form posts to /logout, which redirects to the provider.
	// Chromium applies form-action to that redirect, so the IdP origin must be
	// listed or sign-out silently does nothing.
	if !strings.Contains(csp, "form-action 'self' https://login.uni-osnabrueck.de") {
		t.Errorf("CSP form-action does not allow the IdP; got %q", csp)
	}
}

// With no IdP configured the directive must stay locked to 'self'.
func TestFormActionDefaultsToSelf(t *testing.T) {
	r := chi.NewRouter()
	r.Use(handlers.SecurityHeaders(true, ""))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self';") {
		t.Errorf("form-action should be just 'self'; got %q", csp)
	}
}

// HSTS over plain HTTP would pin a scheme the deployment does not serve.
func TestHSTSOnlyWhenSecure(t *testing.T) {
	r := chi.NewRouter()
	r.Use(handlers.SecurityHeaders(false, ""))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS set on insecure deployment: %q", got)
	}
}
