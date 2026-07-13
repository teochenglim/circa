package httpapi

import (
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

// metricsLookback bounds how far back /metrics looks for each series'
// latest point — generous relative to any of this project's own default
// intervals (scrape/collect default to 15s) so a slightly slow-ticking
// source's most recent point is never missed, without scanning a series'
// entire retention on every scrape of this endpoint.
const metricsLookback = 5 * time.Minute

// metricsHandler serves GET /metrics in Prometheus exposition format
// (DESIGN/04 §4.2, DESIGN/09 §9.1), concatenating two independent
// families: circa's own RED self-metrics (selfmetrics.go, gathered from
// prometheus.DefaultGatherer — every package's promauto-registered
// metrics, not just HTTP's) first, then whatever's currently in
// storage (regardless of whether it arrived via scrape, self-collection,
// line protocol, or remote-write). Unlike query_range, the storage half is
// a snapshot of each series' latest value, not a time range — the shape
// every Prometheus-format consumer (an external Prometheus's own scrape
// config, another Circa instance's ingest.scrape) expects.
func metricsHandler(engine *query.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", string(expfmt.FmtText))
		enc := expfmt.NewEncoder(w, expfmt.FmtText)

		if families, err := prometheus.DefaultGatherer.Gather(); err == nil {
			for _, mf := range families {
				if err := enc.Encode(mf); err != nil {
					return
				}
			}
		}

		byName := make(map[string][]storage.SeriesKey)
		for _, key := range engine.Series() {
			byName[key.Name] = append(byName[key.Name], key)
		}
		names := make([]string, 0, len(byName))
		for name := range byName {
			names = append(names, name)
		}
		sort.Strings(names)

		now := time.Now()
		for _, name := range names {
			mf := &dto.MetricFamily{
				Name: proto.String(name),
				Type: dto.MetricType_UNTYPED.Enum(),
			}
			for _, key := range byName[name] {
				results, _, err := engine.QueryRange(key.Name, key.Labels, storage.TierRaw, query.Range{
					Start: now.Add(-metricsLookback), End: now,
				})
				if err != nil || len(results) == 0 || len(results[0].Points) == 0 {
					continue
				}
				latest := results[0].Points[len(results[0].Points)-1]

				labels := make([]*dto.LabelPair, 0, len(key.Labels))
				for k, v := range key.Labels {
					labels = append(labels, &dto.LabelPair{Name: proto.String(k), Value: proto.String(v)})
				}
				sort.Slice(labels, func(i, j int) bool { return labels[i].GetName() < labels[j].GetName() })

				mf.Metric = append(mf.Metric, &dto.Metric{
					Label:       labels,
					Untyped:     &dto.Untyped{Value: proto.Float64(latest.Value)},
					TimestampMs: proto.Int64(latest.Time.UnixMilli()),
				})
			}
			if len(mf.Metric) == 0 {
				continue
			}
			if err := enc.Encode(mf); err != nil {
				return // client likely disconnected mid-write; nothing more to do
			}
		}
	}
}
