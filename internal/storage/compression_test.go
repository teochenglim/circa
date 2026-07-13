package storage

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCompressionRatioAgainstRawBaseline simulates a realistic scrape
// scenario (node_exporter-shaped metrics: some constant, some a slow random
// walk) and compares actual on-disk bytes used against the v0.1.0 baseline
// of 16 raw bytes per (timestamp, value) point — DESIGN/03 §3.3 asks this be
// measured and recorded, not just assumed from the isolated encoder test.
func TestCompressionRatioAgainstRawBaseline(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 2*time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const interval = 15 * time.Second
	const numPoints = 480 // 2h at 15s, one full tier-0 retention window
	rng := rand.New(rand.NewSource(42))

	series := []struct {
		key   SeriesKey
		value func(i int, prev float64) float64
	}{
		{SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}, func(i int, prev float64) float64 { return 1 }},
		{SeriesKey{Name: "node_cpu_seconds_total", Labels: map[string]string{"cpu": "0", "mode": "idle"}}, func(i int, prev float64) float64 { return prev + 14.9 }},
		{SeriesKey{Name: "node_memory_MemAvailable_bytes", Labels: nil}, func(i int, prev float64) float64 { return prev + rng.Float64()*1e6 - 5e5 }},
	}

	base := time.Now().Truncate(time.Second)
	var totalPoints int
	for _, sr := range series {
		value := 0.0
		for i := 0; i < numPoints; i++ {
			value = sr.value(i, value)
			ts := base.Add(time.Duration(i) * interval)
			if err := s.Append(sr.key, interval, ts, value); err != nil {
				t.Fatalf("Append: %v", err)
			}
			totalPoints++
		}
	}

	var actualBytes int64
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			actualBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking storage dir: %v", err)
	}

	rawBytes := int64(totalPoints * 16)
	ratio := float64(rawBytes) / float64(actualBytes)
	t.Logf("%d points across %d series: raw baseline=%d bytes, actual on-disk=%d bytes (%.2fx)",
		totalPoints, len(series), rawBytes, actualBytes, ratio)

	// Conservative floor: real gains vary with per-series metadata/chunk
	// overhead, but should still comfortably beat 1x for this realistic mix.
	if ratio <= 1.5 {
		t.Errorf("expected meaningful compression over the raw 16-bytes/point baseline, got only %.2fx (raw=%d, actual=%d)", ratio, rawBytes, actualBytes)
	}
}
