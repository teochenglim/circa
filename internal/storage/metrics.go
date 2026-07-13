package storage

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RED self-metrics for the storage layer — writes (traffic), write errors,
// and write latency (delay). Registered globally via promauto, gathered
// into GET /metrics alongside every other package's own self-metrics (see
// internal/httpapi/selfmetrics.go's doc comment for why a shared global
// registry, not a threaded-through one, is used here).
var (
	writesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "circa_storage_writes_total",
		Help: "Total samples successfully written to the raw tier (and rolled up into minute/hour tiers where enabled).",
	})
	writeErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "circa_storage_write_errors_total",
		Help: "Total samples that failed to write.",
	})
	writeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "circa_storage_write_duration_seconds",
		Help:    "Latency of one TieredStore.Consume call (raw append + minute/hour rollup accumulation).",
		Buckets: prometheus.DefBuckets,
	})
	queryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "circa_storage_query_duration_seconds",
		Help:    "Latency of one TieredStore.QueryRange call, by tier.",
		Buckets: prometheus.DefBuckets,
	}, []string{"tier"})
)

func observeWrite(start time.Time, err error) {
	writeDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		writeErrorsTotal.Inc()
		return
	}
	writesTotal.Inc()
}

func tierLabel(tier Tier) string {
	switch tier {
	case TierMinute:
		return "minute"
	case TierHour:
		return "hour"
	default:
		return "raw"
	}
}
