// Package metrics exposes Prometheus instrumentation for the portal.
//
// Scraped by the central monitoring host over HTTPS; Caddy restricts /metrics
// to that host's IPs, so the endpoint itself is unauthenticated.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aiselfservice_http_requests_total",
		Help: "HTTP requests by route template, method and status.",
	}, []string{"route", "method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "aiselfservice_http_request_duration_seconds",
		Help:    "HTTP request latency by route template.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	// KeyOperations counts issuance and revocation separately from HTTP status,
	// because a failed upstream call can still return a redirect to the user.
	KeyOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aiselfservice_key_operations_total",
		Help: "API key operations by action and outcome.",
	}, []string{"action", "outcome"})

	// Gauges are refreshed from the database rather than incremented, so a
	// restart cannot leave them drifting from reality.
	activeKeys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "aiselfservice_active_keys",
		Help: "API keys currently issued.",
	})

	expiringKeys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "aiselfservice_keys_expiring_7d",
		Help: "Issued keys expiring within seven days.",
	})
)

// init pre-registers the label combinations so each series exists at zero from
// startup. A CounterVec otherwise emits nothing until first use, which makes a
// dashboard panel read "no data" whether the portal is idle or broken.
func init() {
	for _, action := range []string{"generate", "extend", "delete", "revoke"} {
		for _, outcome := range []string{"success", "provider_error", "store_error"} {
			KeyOperations.WithLabelValues(action, outcome)
		}
	}
}

// Handler serves the Prometheus exposition format.
func Handler() http.Handler { return promhttp.Handler() }

// SetKeyGauges publishes the current key counts.
func SetKeyGauges(active, expiringWithin7d int) {
	activeKeys.Set(float64(active))
	expiringKeys.Set(float64(expiringWithin7d))
}

// statusRecorder captures the status code for the middleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Middleware records request counts and latency.
//
// routeOf must return the route *template* (e.g. "/admin/users/{id}/profile")
// rather than the concrete path, or every user id would create a new time
// series and blow up cardinality.
func Middleware(routeOf func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			route := routeOf(r)
			if route == "" {
				route = "other"
			}
			requestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
			requestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		})
	}
}
