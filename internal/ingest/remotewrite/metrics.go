package remotewrite

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Receiver self-metrics: request-level traffic/errors/duration are already
// captured generically by internal/httpapi's instrumentHTTP (the receiver
// is served as a normal POST route) — receiveSamplesTotal adds the one
// thing that generic HTTP instrumentation can't see: how many samples were
// actually decoded out of each request body, since a single POST can carry
// anywhere from one to thousands.
var receiveSamplesTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "circa_remotewrite_receive_samples_total",
	Help: "Total samples decoded from incoming remote-write POST bodies.",
})

// Sender self-metrics: full RED, since the outbound sender isn't behind
// internal/httpapi at all — it's this process acting as an HTTP *client*.
var (
	sendTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_remotewrite_send_total",
		Help: "Total outbound remote-write pushes, by result.",
	}, []string{"result"}) // "ok" | "error"
	sendDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "circa_remotewrite_send_duration_seconds",
		Help:    "Latency of one outbound remote-write push.",
		Buckets: prometheus.DefBuckets,
	})
)

func observeSend(start time.Time, err error) {
	sendDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		sendTotal.WithLabelValues("error").Inc()
		return
	}
	sendTotal.WithLabelValues("ok").Inc()
}
