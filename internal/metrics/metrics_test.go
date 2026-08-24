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
