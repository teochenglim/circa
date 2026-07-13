package anomaly

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RED self-metrics for the anomaly detector: scoring (traffic/delay —
// Score has no real "error" outcome, just anomalous/not-yet-enough-data)
// and retraining (full RED, since Train and the QueryRange it depends on
// can genuinely fail).
var (
	scoresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "circa_anomaly_scores_total",
		Help: "Total Score calls (one per sample circa scores when features.ml is on).",
	})
	scoreDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "circa_anomaly_score_duration_seconds",
		Help:    "Latency of one Score call.",
		Buckets: prometheus.DefBuckets,
	})

	retrainsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_anomaly_retrains_total",
		Help: "Total per-series retrain attempts, by result.",
	}, []string{"result"}) // "ok" | "skipped" (not enough data yet) | "error"
	retrainDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "circa_anomaly_retrain_duration_seconds",
		Help:    "Latency of one series' retrain attempt (query + preprocess + train).",
		Buckets: prometheus.DefBuckets,
	})
)

func observeScore(start time.Time) {
	scoresTotal.Inc()
	scoreDuration.Observe(time.Since(start).Seconds())
}

func observeRetrain(start time.Time, result string) {
	retrainDuration.Observe(time.Since(start).Seconds())
	retrainsTotal.WithLabelValues(result).Inc()
}
