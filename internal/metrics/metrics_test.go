package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Every key-operation series must exist at zero before any traffic, so a
// dashboard distinguishes "idle" from "broken".
func TestKeyOperationSeriesPreRegistered(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`aiselfservice_key_operations_total{action="generate",outcome="success"} 0`,
		`aiselfservice_key_operations_total{action="revoke",outcome="provider_error"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing pre-registered series: %s", want)
		}
	}
}

// Scrapers that negotiate OpenMetrics get it; everyone else still gets the
// Prometheus text format.
func TestHandlerNegotiatesOpenMetrics(t *testing.T) {
	for _, tc := range []struct{ accept, want string }{
		{"application/openmetrics-text; version=1.0.0", "application/openmetrics-text"},
		{"", "text/plain"},
	} {
		req := httptest.NewRequest("GET", "/metrics", nil)
		if tc.accept != "" {
			req.Header.Set("Accept", tc.accept)
		}
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.want) {
			t.Errorf("Accept %q: Content-Type = %q, want %s", tc.accept, ct, tc.want)
		}
		if tc.accept != "" && !strings.HasSuffix(rec.Body.String(), "# EOF\n") {
			t.Errorf("OpenMetrics body missing # EOF terminator")
		}
	}
}
