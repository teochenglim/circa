package backup

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RED self-metrics for the CDC export cycle (DESIGN/07 §7.2) — one
// exportOnce run is the natural unit of "traffic" here, whether it ran in
// push mode (in-process) or as one of the pull-mode agent's per-node
// pollers.
var (
	exportsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "circa_backup_exports_total",
		Help: "Total export cycle runs, by result.",
	}, []string{"result"}) // "ok" | "empty" (nothing new) | "error"
	exportDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "circa_backup_export_duration_seconds",
		Help:    "Latency of one export cycle (watermark load + delta read + iceberg append + watermark save).",
		Buckets: prometheus.DefBuckets,
	})
	rowsExportedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "circa_backup_rows_exported_total",
		Help: "Total rows appended to the Iceberg table.",
	})
)

func observeExport(start time.Time, rows int, result string) {
	exportDuration.Observe(time.Since(start).Seconds())
	exportsTotal.WithLabelValues(result).Inc()
	if result == "ok" {
		rowsExportedTotal.Add(float64(rows))
	}
}
