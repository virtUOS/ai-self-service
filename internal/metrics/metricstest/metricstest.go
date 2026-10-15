// Package metricstest reads the portal's metrics back the way a scraper would,
// for tests that check a series moved.
package metricstest

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/virtuos/ai-self-service/internal/metrics"
)

// Counter returns the current value of one series, such as
// `aiselfservice_keys_missing_total{source="sync"}`, or 0 when it is absent.
// Labels are given as they appear in the exposition, in label-name order.
func Counter(t testing.TB, name, labels string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	prefix := name + "{" + labels + "} "
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return v
	}
	return 0
}
