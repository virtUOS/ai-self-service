package metrics

import (
	"context"
	"errors"
	"net/http"
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
		`keyosk_key_operations_total{action="generate",outcome="success"} 0`,
		`keyosk_key_operations_total{action="revoke",outcome="provider_error"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing pre-registered series: %s", want)
		}
	}
}

// Keys the gateway no longer has are counted per place the portal noticed, and
// every series exists at zero: the dashboard panel is an alarm for a gateway or
// configuration problem, so "no data" must never be mistaken for "none"
// (issue #108).
func TestMissingKeySeriesPreRegistered(t *testing.T) {
	CountMissingKeysWith(func(context.Context) (int, error) { return 0, nil })
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	var want []string
	for _, source := range []string{SourceDashboard, SourceExtend, SourceSync} {
		want = append(want, `keyosk_keys_marked_missing_total{source="`+source+`"} 0`)
	}
	want = append(want,
		`keyosk_keys_missing 0`,
		`keyosk_keys_missing_removed_total 0`)
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing pre-registered series: %s", w)
		}
	}
}

// The gauge of marked keys is counted when it is scraped, so it is exact
// whatever marked, unmarked or removed a key. A count that fails leaves the
// gauge out of that scrape instead of failing the scrape or reporting zero.
func TestMissingKeysGaugeCountsOnScrape(t *testing.T) {
	scrape := func() string {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		if rec.Code != 200 {
			t.Fatalf("scrape status = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}

	n := 3
	CountMissingKeysWith(func(context.Context) (int, error) { return n, nil })
	if body := scrape(); !strings.Contains(body, "\nkeyosk_keys_missing 3\n") {
		t.Errorf("gauge does not report 3:\n%s", body)
	}
	n = 1
	if body := scrape(); !strings.Contains(body, "\nkeyosk_keys_missing 1\n") {
		t.Errorf("gauge does not follow the count to 1:\n%s", body)
	}

	CountMissingKeysWith(func(context.Context) (int, error) { return 0, errors.New("database is locked") })
	body := scrape()
	if strings.Contains(body, "\nkeyosk_keys_missing ") {
		t.Errorf("gauge reported despite a failed count:\n%s", body)
	}
	if !strings.Contains(body, "keyosk_active_keys ") {
		t.Errorf("a failed count took other series with it:\n%s", body)
	}
}

// OpenMetrics names a counter's family without its _total suffix, so a counter
// foo_total and a gauge foo would be one family declared twice, against the
// spec and easily confused in a query. The Go runtime's own metrics have such
// pairs; Keyosk's do not.
func TestOpenMetricsFamiliesAreUnique(t *testing.T) {
	CountMissingKeysWith(func(context.Context) (int, error) { return 0, nil })
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	seen := map[string]bool{}
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "#" || fields[1] != "TYPE" || !strings.HasPrefix(fields[2], "keyosk_") {
			continue
		}
		if seen[fields[2]] {
			t.Errorf("metric family %s is declared twice", fields[2])
		}
		seen[fields[2]] = true
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

// With a token, only a request carrying it as a bearer token gets the metrics.
func TestRequireToken(t *testing.T) {
	h := RequireToken("s3cret")(Handler())
	for _, tc := range []struct {
		auth string
		want int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Bearer s3cret-and-more", http.StatusUnauthorized},
		{"Bearer ", http.StatusUnauthorized},
		{"Basic czNjcmV0", http.StatusUnauthorized},
		{"s3cret", http.StatusUnauthorized},
		{"Bearer s3cret", http.StatusOK},
		{"bearer s3cret", http.StatusOK},
	} {
		req := httptest.NewRequest("GET", "/metrics", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("Authorization %q: %d, want %d", tc.auth, rec.Code, tc.want)
		}
		if rec.Code == http.StatusUnauthorized && !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("Authorization %q: WWW-Authenticate = %q, want a Bearer challenge",
				tc.auth, rec.Header().Get("WWW-Authenticate"))
		}
		if rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "keyosk_") {
			t.Errorf("Authorization %q: refused response carries metrics", tc.auth)
		}
	}
}

// Without a token the endpoint stays open, as it was before there was one.
func TestRequireTokenEmptyLetsEveryoneThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	RequireToken("")(Handler()).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("no token configured: %d, want 200", rec.Code)
	}
}
