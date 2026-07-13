package collect

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RED self-metrics for the built-in local collector (v0.5.0+).
var (
	ticksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_collect_ticks_total",
		Help: "Total local-collection ticks, by result.",
	}, []string{"result"}) // "ok" | "error"
	tickDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "circa_collect_duration_seconds",
		Help:    "Latency of one local-collection tick (collectAll).",
		Buckets: prometheus.DefBuckets,
	})
	samplesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "circa_collect_samples_total",
		Help: "Total samples produced by the local collector.",
	})
)

func observeCollect(start time.Time, sampleCount int, err error) {
	tickDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		ticksTotal.WithLabelValues("error").Inc()
		return
	}
	ticksTotal.WithLabelValues("ok").Inc()
	samplesTotal.Add(float64(sampleCount))
}
