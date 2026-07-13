package storage

import (
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

func TestTieredStoreRollsUpIntoMinuteTier(t *testing.T) {
	dir := t.TempDir()
	ts, err := OpenTiered(dir, time.Hour, time.Hour, 0)
	if err != nil {
		t.Fatalf("OpenTiered: %v", err)
	}
	defer ts.Close()

	if ts.Minute == nil {
		t.Fatal("expected minute tier to be enabled")
	}
	if ts.Hour != nil {
		t.Fatal("expected hour tier to be disabled (retention 0)")
	}

	key := SeriesKey{Name: "cpu", Labels: map[string]string{"host": "a"}}
	interval := 15 * time.Second
	// Two full minute buckets' worth of samples (4 per minute at 15s),
	// plus one sample into a third bucket to force the second bucket to flush.
	base := time.Unix(0, 0).Truncate(time.Minute)
	values := []float64{
		10, 20, 30, 40, // minute 0: min=10 avg=25 max=40
		1, 2, 3, 4, // minute 1: min=1 avg=2.5 max=4
		99, // minute 2: forces minute 1 to flush
	}
	for i, v := range values {
		ts.consumeAt(t, key, interval, base.Add(time.Duration(i)*interval), v)
	}

	raw, agg, err := ts.QueryRange("cpu", map[string]string{"host": "a"}, TierMinute, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if raw != nil {
		t.Fatalf("expected nil raw results for TierMinute, got %v", raw)
	}
	if len(agg) != 1 {
		t.Fatalf("expected 1 series, got %d", len(agg))
	}
	points := agg[0].Points
	if len(points) != 2 {
		t.Fatalf("expected 2 flushed buckets, got %d: %+v", len(points), points)
	}

	if points[0].Min != 10 || points[0].Avg != 25 || points[0].Max != 40 {
		t.Errorf("bucket 0 = %+v, want min=10 avg=25 max=40", points[0])
	}
	if points[1].Min != 1 || points[1].Avg != 2.5 || points[1].Max != 4 {
		t.Errorf("bucket 1 = %+v, want min=1 avg=2.5 max=4", points[1])
	}
}

func TestTieredStoreRawTierUnaffectedByRollups(t *testing.T) {
	dir := t.TempDir()
	ts, err := OpenTiered(dir, time.Hour, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("OpenTiered: %v", err)
	}
	defer ts.Close()

	key := SeriesKey{Name: "up"}
	now := time.Now()
	if err := ts.Consume(sampleFor(key, time.Second, now, 1)); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	raw, agg, err := ts.QueryRange("up", nil, TierRaw, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if agg != nil {
		t.Fatalf("expected nil agg results for TierRaw, got %v", agg)
	}
	if len(raw) != 1 || len(raw[0].Points) != 1 {
		t.Fatalf("expected 1 raw point, got %+v", raw)
	}

	// The raw series list should not be polluted by internal "#min"/"#avg"/"#max"
	// rollup sub-series names — those live in the minute/hour tier's own
	// stores, never in Raw.
	if got := ts.Series(); len(got) != 1 || got[0].Name != "up" {
		t.Errorf("Series() = %+v, want exactly [{Name: up}]", got)
	}
}

func TestQueryRangeDisabledTierReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	ts, err := OpenTiered(dir, time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("OpenTiered: %v", err)
	}
	defer ts.Close()

	now := time.Now()
	_, agg, err := ts.QueryRange("anything", nil, TierMinute, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if agg != nil {
		t.Errorf("expected nil results when minute tier is disabled, got %v", agg)
	}
}

func sampleFor(key SeriesKey, interval time.Duration, t time.Time, value float64) ingest.Sample {
	return ingest.Sample{Name: key.Name, Labels: key.Labels, Time: t, Value: value, Interval: interval}
}

func (ts *TieredStore) consumeAt(t *testing.T, key SeriesKey, interval time.Duration, at time.Time, value float64) {
	t.Helper()
	if err := ts.Consume(sampleFor(key, interval, at, value)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
}
