// Package metrics exposes Prometheus instrumentation for Keyosk.
//
// Scraped by the central monitoring host over HTTPS; Caddy restricts /metrics
// to that host's IPs, so the endpoint itself is unauthenticated.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Where the portal can first notice that the gateway no longer has a key. They
// are the label values of KeysMarkedMissing, and the text the audit log and the log
// say it was noticed by.
const (
	SourceDashboard = "dashboard"
	SourceExtend    = "extend"
	SourceSync      = "sync"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "keyosk_http_requests_total",
		Help: "HTTP requests by route template, method and status.",
	}, []string{"route", "method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "keyosk_http_request_duration_seconds",
		Help:    "HTTP request latency by route template.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	// KeyOperations counts issuance and revocation separately from HTTP status,
	// because a failed upstream call can still return a redirect to the user.
	KeyOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "keyosk_key_operations_total",
		Help: "API key operations by action and outcome.",
	}, []string{"action", "outcome"})

	// KeysMarkedMissing counts keys the gateway reported as gone, by where
	// Keyosk first noticed: the dashboard, an extend, or the limit sync. The
	// key is marked, not removed, and counted once per marking, however often
	// it is seen again. A key deleted in the gateway on purpose is rare; a run
	// of them means an instance pointed at the wrong gateway or a restored
	// gateway database, which this is the early warning for (issue #108).
	//
	// It is not called keys_missing_total: in OpenMetrics that would be the
	// same metric family as the keys_missing gauge, and the two would be easy
	// to confuse in a query.
	KeysMarkedMissing = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "keyosk_keys_marked_missing_total",
		Help: "Keys marked as missing because the gateway no longer had them, by where Keyosk noticed.",
	}, []string{"source"})

	// KeysMissingRemoved counts marked keys whose record the sync removed
	// because they stayed missing past the grace period. That is the damage
	// done: the owner has lost the key.
	KeysMissingRemoved = promauto.NewCounter(prometheus.CounterOpts{
		Name: "keyosk_keys_missing_removed_total",
		Help: "Keys removed after staying missing from the gateway past the grace period.",
	})

	// Gauges are refreshed from the database rather than incremented, so a
	// restart cannot leave them drifting from reality.
	activeKeys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "keyosk_active_keys",
		Help: "API keys currently issued.",
	})

	expiringKeys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "keyosk_keys_expiring_7d",
		Help: "Issued keys expiring within seven days.",
	})
)

// init pre-registers the label combinations so each series exists at zero from
// startup. A CounterVec otherwise emits nothing until first use, which makes a
// dashboard panel read "no data" whether Keyosk is idle or broken.
func init() {
	for _, action := range []string{"generate", "extend", "delete", "revoke"} {
		for _, outcome := range []string{"success", "provider_error", "store_error"} {
			KeyOperations.WithLabelValues(action, outcome)
		}
	}
	for _, source := range []string{SourceDashboard, SourceExtend, SourceSync} {
		KeysMarkedMissing.WithLabelValues(source)
	}
	prometheus.MustRegister(missingKeys{prometheus.NewDesc("keyosk_keys_missing",
		"Keys marked as missing from the gateway, waiting out the grace period.", nil, nil)})
}

// Handler serves the OpenMetrics format to scrapers that ask for it and falls
// back to the Prometheus text format otherwise.
func Handler() http.Handler {
	return promhttp.InstrumentMetricHandler(prometheus.DefaultRegisterer,
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		}))
}

// SetKeyGauges publishes the current key counts.
func SetKeyGauges(active, expiringWithin7d int) {
	activeKeys.Set(float64(active))
	expiringKeys.Set(float64(expiringWithin7d))
}

// countMissing is what the keys_missing gauge reads on every scrape.
var countMissing atomic.Pointer[func(context.Context) (int, error)]

// CountMissingKeysWith sets how the keys_missing gauge counts the keys marked
// as missing. It is counted on every scrape, so the gauge is exact however and
// wherever a key was marked, unmarked or removed. Until it is set, the gauge
// is left out.
func CountMissingKeysWith(count func(context.Context) (int, error)) {
	countMissing.Store(&count)
}

// missingKeys is the keys_missing gauge, counted when it is scraped.
type missingKeys struct{ desc *prometheus.Desc }

func (m missingKeys) Describe(ch chan<- *prometheus.Desc) { ch <- m.desc }

// Collect leaves the gauge out of a scrape whose count fails, rather than
// failing the scrape or reporting a count it does not have.
func (m missingKeys) Collect(ch chan<- prometheus.Metric) {
	count := countMissing.Load()
	if count == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := (*count)(ctx)
	if err != nil {
		slog.Error("metrics: count missing keys", "err", err)
		return
	}
	ch <- prometheus.MustNewConstMetric(m.desc, prometheus.GaugeValue, float64(n))
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
