package httpapi

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// selfMetricPoint is one flattened self-metric reading — a histogram/summary
// contributes two of these (its _sum and _count), everything else
// contributes one, mirroring the series names a Prometheus client would
// derive rate()/avg() from.
type selfMetricPoint struct {
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Value      float64           `json:"value"`
	Cumulative bool              `json:"cumulative"` // true for counters (incl. histogram/summary sum+count) - client should rate() these, not chart the raw value
}

// selfMetricsHandler serves GET /api/v1/selfmetrics — a JSON snapshot of
// circa's own RED self-metrics (ARCHITECTURE.md "Self-metrics", v0.7.0),
// gathered live from prometheus.DefaultGatherer the same way GET /metrics
// does (metrics.go). Unlike /metrics (Prometheus exposition format for
// scraping), this shape is for the dashboard's Self-metrics page (v1.0.0) to
// poll and build a short client-side rolling history from — there's
// deliberately no server-side time series for these, so each poll reflects
// real, current process state, never simulated data (see
// web/static/js/selfmetrics.js).
func selfMetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		families, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		points := make([]selfMetricPoint, 0, len(families))
		for _, mf := range families {
			for _, m := range mf.Metric {
				labels := labelMap(m.Label)
				switch mf.GetType() {
				case dto.MetricType_COUNTER:
					points = append(points, selfMetricPoint{Name: mf.GetName(), Labels: labels, Value: m.GetCounter().GetValue(), Cumulative: true})
				case dto.MetricType_GAUGE:
					points = append(points, selfMetricPoint{Name: mf.GetName(), Labels: labels, Value: m.GetGauge().GetValue()})
				case dto.MetricType_HISTOGRAM:
					h := m.GetHistogram()
					points = append(points,
						selfMetricPoint{Name: mf.GetName() + "_sum", Labels: labels, Value: h.GetSampleSum(), Cumulative: true},
						selfMetricPoint{Name: mf.GetName() + "_count", Labels: labels, Value: float64(h.GetSampleCount()), Cumulative: true},
					)
				case dto.MetricType_SUMMARY:
					s := m.GetSummary()
					points = append(points,
						selfMetricPoint{Name: mf.GetName() + "_sum", Labels: labels, Value: s.GetSampleSum(), Cumulative: true},
						selfMetricPoint{Name: mf.GetName() + "_count", Labels: labels, Value: float64(s.GetSampleCount()), Cumulative: true},
					)
				default:
					points = append(points, selfMetricPoint{Name: mf.GetName(), Labels: labels, Value: m.GetUntyped().GetValue()})
				}
			}
		}

		writeJSON(w, http.StatusOK, struct {
			Status string            `json:"status"`
			Data   []selfMetricPoint `json:"data"`
		}{Status: "success", Data: points})
	}
}

func labelMap(pairs []*dto.LabelPair) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	labels := make(map[string]string, len(pairs))
	for _, p := range pairs {
		labels[p.GetName()] = p.GetValue()
	}
	return labels
}
