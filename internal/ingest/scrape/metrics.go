package scrape

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RED self-metrics for the scrape source, labeled by target URL — bounded
// cardinality since ingest.scrape.targets is a small, admin-configured
// list, not user input. See internal/httpapi/selfmetrics.go's doc comment
// for why these register into the shared global registry.
var (
	scrapesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_scrape_requests_total",
		Help: "Total scrape attempts per target.",
	}, []string{"target"})
	scrapeErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_scrape_errors_total",
		Help: "Total failed scrape attempts per target.",
	}, []string{"target"})
	scrapeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "circa_scrape_duration_seconds",
		Help:    "Scrape request latency per target.",
		Buckets: prometheus.DefBuckets,
	}, []string{"target"})
)

func observeScrape(target string, start time.Time, err error) {
	scrapesTotal.WithLabelValues(target).Inc()
	scrapeDuration.WithLabelValues(target).Observe(time.Since(start).Seconds())
	if err != nil {
		scrapeErrorsTotal.WithLabelValues(target).Inc()
	}
}
