package query

import (
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/storage"
)

func TestQueryRangeReturnsMetricLabelsIncludingName(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.OpenTiered(dir, time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	now := time.Now()
	key := storage.SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}
	if err := store.Raw.Append(key, time.Second, now, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	e := New(store)
	results, agg, err := e.QueryRange("up", nil, storage.TierRaw, Range{Start: now.Add(-time.Minute), End: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if agg != nil {
		t.Fatalf("expected nil agg results for TierRaw, got %v", agg)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Metric["__name__"] != "up" || results[0].Metric["job"] != "node" {
		t.Errorf("Metric = %+v", results[0].Metric)
	}
	if len(results[0].Points) != 1 || results[0].Points[0].Value != 1 {
		t.Errorf("Points = %+v", results[0].Points)
	}
}

func TestQueryRangeNoMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.OpenTiered(dir, time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	e := New(store)
	results, agg, err := e.QueryRange("nonexistent", nil, storage.TierRaw, Range{Start: time.Now().Add(-time.Hour), End: time.Now()})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if agg != nil {
		t.Fatalf("expected nil agg results for TierRaw, got %v", agg)
	}
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

func TestQueryRangeMinuteTierReturnsAggResults(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.OpenTiered(dir, time.Hour, time.Hour, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	base := time.Unix(0, 0).Truncate(time.Minute)
	key := storage.SeriesKey{Name: "cpu"}
	// The first 3 samples land in minute bucket 0 ([0,60)); the last, at
	// t=60s, lands in bucket 1 and forces bucket 0 to flush.
	offsetsSec := []int{0, 15, 30, 60}
	values := []float64{10, 20, 30, 99}
	for i, v := range values {
		sample := ingest.Sample{Name: key.Name, Time: base.Add(time.Duration(offsetsSec[i]) * time.Second), Value: v, Interval: 15 * time.Second}
		if err := store.Consume(sample); err != nil {
			t.Fatalf("Consume: %v", err)
		}
	}

	e := New(store)
	results, agg, err := e.QueryRange("cpu", nil, storage.TierMinute, Range{Start: base.Add(-time.Hour), End: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil raw results for TierMinute, got %v", results)
	}
	if len(agg) != 1 {
		t.Fatalf("expected 1 agg series, got %d: %+v", len(agg), agg)
	}
	if agg[0].Metric["__name__"] != "cpu" {
		t.Errorf("Metric = %+v", agg[0].Metric)
	}
	if len(agg[0].Points) != 1 {
		t.Fatalf("expected 1 flushed bucket, got %d: %+v", len(agg[0].Points), agg[0].Points)
	}
	if p := agg[0].Points[0]; p.Min != 10 || p.Avg != 20 || p.Max != 30 {
		t.Errorf("bucket = %+v, want min=10 avg=20 max=30", p)
	}
}
