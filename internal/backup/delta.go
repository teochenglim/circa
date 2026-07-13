package backup

import (
	"context"
	"time"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

// CollectDelta reads every currently-known series' tier-1 (minute-rollup)
// points in (since, now] and flattens them into export Rows — the shared
// mechanism DESIGN/07 §7.3 calls for. Both LocalSource (push mode, calling
// this directly) and internal/httpapi's GET /api/v1/backup_range handler
// (pull mode, calling this to serve a remote Exporter) use it identically.
//
// since is an exclusive lower bound — a point exactly at since was already
// exported by whichever run advanced the watermark to since, so re-reading
// it here (QueryRange's own bounds are inclusive) and re-exporting it would
// double-count that point in the Iceberg table.
func CollectDelta(engine *query.Engine, nodeID, hostname string, since, now time.Time) ([]Row, time.Time) {
	newWatermark := since
	var rows []Row

	for _, key := range engine.Series() {
		_, aggResults, err := engine.QueryRange(key.Name, key.Labels, storage.TierMinute, query.Range{Start: since, End: now})
		if err != nil {
			continue
		}
		for _, res := range aggResults {
			name := res.Metric["__name__"]
			labels := make(map[string]string, len(res.Metric))
			for k, v := range res.Metric {
				if k != "__name__" {
					labels[k] = v
				}
			}
			for _, p := range res.Points {
				if !p.Time.After(since) {
					continue
				}
				rows = append(rows, Row{
					NodeID:     nodeID,
					Hostname:   hostname,
					MetricName: name,
					Labels:     labels,
					Time:       p.Time,
					Value:      p.Avg,
					Anomalous:  false,
				})
				if p.Time.After(newWatermark) {
					newWatermark = p.Time
				}
			}
		}
	}
	return rows, newWatermark
}

// LocalSource is the push-mode DeltaSource: CollectDelta runs directly
// in-process against this node's own query.Engine, no HTTP hop.
type LocalSource struct {
	Engine   *query.Engine
	NodeID   string
	Hostname string
}

func (s *LocalSource) DeltaRange(ctx context.Context, since time.Time) ([]Row, time.Time, error) {
	rows, watermark := CollectDelta(s.Engine, s.NodeID, s.Hostname, since, time.Now())
	return rows, watermark, nil
}
