package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTP self-metrics — the RED (rate/errors/duration) golden signals for
// circa's own API surface, per DESIGN/05's previously-unspecified
// "self-metrics panel." Registered via promauto into
// prometheus.DefaultRegisterer, the same global registry every other
// package's own RED metrics (internal/ingest/scrape, internal/storage,
// internal/alert, internal/anomaly, internal/backup) register into — one
// shared registry, no threading a *prometheus.Registry through every
// constructor in the codebase. metricsHandler (metrics.go) gathers from
// prometheus.DefaultGatherer, so every package's self-metrics show up in
// GET /metrics automatically, including ones added after this file.
var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_http_requests_total",
		Help: "Total HTTP requests served by circa's own API, by method, path, and status — traffic and (via status=~\"5..\") errors.",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "circa_http_request_duration_seconds",
		Help:    "circa's own HTTP request latency, by method and path — the \"delay\" golden signal.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

// instrumentHTTP wraps next, recording every request's method/path/status
// and latency — labeled by the request's own path directly (not a
// wildcarded pattern), which is safe here since every route circa serves
// is a fixed, small set (no user-supplied path segments like `/things/{id}`
// that would blow up cardinality).
func instrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusCapturingWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		duration := time.Since(start).Seconds()
		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(sw.status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
	})
}

// statusCapturingWriter records the status code a handler actually wrote,
// defaulting to 200 (matching net/http's own behavior when WriteHeader is
// never called explicitly).
type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
