package backup

import (
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

func mustOpenStore(t *testing.T) *storage.TieredStore {
	t.Helper()
	ts, err := storage.OpenTiered(t.TempDir(), time.Hour, time.Hour, 0)
	if err != nil {
		t.Fatalf("OpenTiered: %v", err)
	}
	t.Cleanup(func() { ts.Close() })
	return ts
}

// TestCollectDelta_ExcludesSinceExclusiveAndAdvancesWatermark writes two
// flushed minute buckets (per storage's own rollup test pattern — a bucket
// only flushes once a later point crosses into the next one) and confirms
// CollectDelta with since set to the first bucket's timestamp returns only
// the second bucket, not both — the exclusive-lower-bound behavior that
// prevents double-exporting the point a prior run's watermark already
// covered.
func TestCollectDelta_ExcludesSinceExclusiveAndAdvancesWatermark(t *testing.T) {
	ts := mustOpenStore(t)
	engine := query.New(ts)

	interval := 15 * time.Second
	base := time.Unix(0, 0).Truncate(time.Minute)
	key := storage.SeriesKey{Name: "cpu", Labels: map[string]string{"host": "a"}}

	values := []float64{10, 20, 30, 40, 1, 2, 3, 4, 99}
	for i, v := range values {
		ts.Consume(ingest.Sample{
			Name: key.Name, Labels: key.Labels,
			Time: base.Add(time.Duration(i) * interval), Value: v, Interval: interval,
		})
	}

	// Bucket 0 (minute 0, avg=25) and bucket 1 (minute 1, avg=2.5) are both
	// flushed now (the 9th sample, minute 2, forced bucket 1 to close).
	sinceBucket0 := base // exactly bucket 0's timestamp

	rows, watermark := CollectDelta(engine, "node-1", "host-a", sinceBucket0, time.Now())
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (bucket 0 excluded, bucket 1 included): %+v", len(rows), rows)
	}
	if rows[0].Value != 2.5 {
		t.Errorf("row value = %v, want 2.5 (bucket 1's avg)", rows[0].Value)
	}
	if rows[0].NodeID != "node-1" || rows[0].Hostname != "host-a" {
		t.Errorf("row identity = %+v, want node-1/host-a", rows[0])
	}
	if rows[0].MetricName != "cpu" {
		t.Errorf("MetricName = %q, want cpu", rows[0].MetricName)
	}
	if rows[0].Labels["host"] != "a" {
		t.Errorf("Labels = %+v, want host=a", rows[0].Labels)
	}
	if rows[0].Anomalous {
		t.Error("Anomalous should always be false for tier-1 exports")
	}
	if !watermark.Equal(rows[0].Time) {
		t.Errorf("watermark = %v, want %v (the last exported row's time)", watermark, rows[0].Time)
	}
}

// TestCollectDelta_NothingNewReturnsUnchangedWatermark confirms a since
// already past every flushed bucket returns no rows and leaves the
// watermark exactly as passed in — Exporter relies on this to skip
// persisting a no-op run.
func TestCollectDelta_NothingNewReturnsUnchangedWatermark(t *testing.T) {
	ts := mustOpenStore(t)
	engine := query.New(ts)

	future := time.Now().Add(24 * time.Hour)
	rows, watermark := CollectDelta(engine, "node-1", "host-a", future, time.Now())
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
	if !watermark.Equal(future) {
		t.Errorf("watermark = %v, want unchanged %v", watermark, future)
	}
}
