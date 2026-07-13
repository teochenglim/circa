package alert

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RED self-metrics for rule evaluation (traffic/delay — evaluation itself
// can't meaningfully "error," a rule either matches or it doesn't) and
// notifier dispatch (full RED, since Notify is a real network call that
// can fail).
var (
	evaluationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_alert_evaluations_total",
		Help: "Total rule evaluations, by rule name.",
	}, []string{"rule"})
	evaluationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "circa_alert_evaluation_duration_seconds",
		Help:    "Latency of evaluating one rule against one sample.",
		Buckets: prometheus.DefBuckets,
	}, []string{"rule"})

	notifyTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_alert_notify_total",
		Help: "Total notification dispatches, by notifier and result.",
	}, []string{"notifier", "result"}) // result: "ok" | "error"
	notifyDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "circa_alert_notify_duration_seconds",
		Help:    "Latency of one notifier's Notify call.",
		Buckets: prometheus.DefBuckets,
	}, []string{"notifier"})
)

func observeEvaluation(rule string, start time.Time) {
	evaluationsTotal.WithLabelValues(rule).Inc()
	evaluationDuration.WithLabelValues(rule).Observe(time.Since(start).Seconds())
}

func observeNotify(notifier string, start time.Time, err error) {
	notifyDuration.WithLabelValues(notifier).Observe(time.Since(start).Seconds())
	if err != nil {
		notifyTotal.WithLabelValues(notifier, "error").Inc()
		return
	}
	notifyTotal.WithLabelValues(notifier, "ok").Inc()
}
