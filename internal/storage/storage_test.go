package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndQueryRangeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}
	base := time.Now().Truncate(time.Second)
	interval := 15 * time.Second

	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * interval)
		if err := s.Append(key, interval, ts, float64(i)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	results, err := s.QueryRange("up", nil, base.Add(-time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 series, got %d", len(results))
	}
	if len(results[0].Points) != 5 {
		t.Fatalf("expected 5 points, got %d: %+v", len(results[0].Points), results[0].Points)
	}
	for i, p := range results[0].Points {
		if p.Value != float64(i) {
			t.Errorf("point %d: value = %v, want %v", i, p.Value, i)
		}
	}
}

func TestQueryRangeFiltersByTimeWindow(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := SeriesKey{Name: "temp"}
	interval := time.Second
	base := time.Now().Truncate(time.Second)
	for i := 0; i < 10; i++ {
		s.Append(key, interval, base.Add(time.Duration(i)*interval), float64(i))
	}

	results, err := s.QueryRange("temp", nil, base.Add(3*time.Second), base.Add(6*time.Second))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(results[0].Points) != 4 { // 3,4,5,6 inclusive
		t.Fatalf("expected 4 points in window, got %d: %+v", len(results[0].Points), results[0].Points)
	}
}

func TestQueryRangeFiltersByLabels(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	interval := time.Second
	now := time.Now()
	s.Append(SeriesKey{Name: "cpu", Labels: map[string]string{"host": "a"}}, interval, now, 1)
	s.Append(SeriesKey{Name: "cpu", Labels: map[string]string{"host": "b"}}, interval, now, 2)

	results, err := s.QueryRange("cpu", map[string]string{"host": "b"}, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 matching series, got %d", len(results))
	}
	if results[0].Key.Labels["host"] != "b" {
		t.Errorf("expected host=b series, got %+v", results[0].Key)
	}
}

func TestRingBufferWrapsAtCapacity(t *testing.T) {
	dir := t.TempDir()
	// retention 4s / interval 1s -> maxPointsPerChunk clamps to 1, so the
	// fixed 8-chunk ring (chunksPerSeries) holds at most 8 one-second slots.
	s, err := Open(dir, 4*time.Second)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := SeriesKey{Name: "wrap"}
	interval := time.Second
	base := time.Now().Truncate(time.Second)
	for i := 0; i < 20; i++ {
		s.Append(key, interval, base.Add(time.Duration(i)*interval), float64(i))
	}

	results, err := s.QueryRange("wrap", nil, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	// Only the last chunksPerSeries writes should still be present after wraparound.
	if len(results[0].Points) > chunksPerSeries {
		t.Fatalf("expected wraparound to cap points at %d, got %d", chunksPerSeries, len(results[0].Points))
	}
	last := results[0].Points[len(results[0].Points)-1]
	if last.Value != 19 {
		t.Errorf("expected most recent point to survive wraparound, got value %v", last.Value)
	}
}

func TestReopenPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	key := SeriesKey{Name: "persist", Labels: map[string]string{"a": "b"}}
	interval := time.Second
	now := time.Now().Truncate(time.Second)

	s1, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s1.Append(key, interval, now, 42); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	results, err := s2.QueryRange("persist", nil, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange after reopen: %v", err)
	}
	if len(results) != 1 || len(results[0].Points) != 1 || results[0].Points[0].Value != 42 {
		t.Fatalf("data did not survive reopen: %+v", results)
	}
}

func TestSeriesKeyStringIsOrderIndependent(t *testing.T) {
	a := SeriesKey{Name: "m", Labels: map[string]string{"x": "1", "y": "2"}}
	b := SeriesKey{Name: "m", Labels: map[string]string{"y": "2", "x": "1"}}
	if a.String() != b.String() {
		t.Errorf("expected identical canonical strings, got %q vs %q", a.String(), b.String())
	}
}

// TestSeriesSurvivesExternalDirDeletion guards against a real bug found in
// manual testing: if a series' on-disk directory disappears out from under
// a running Store (e.g. external cleanup while the process is live),
// QueryRange used to return a hard error instead of treating it as "no
// data," and the next Append never recreated the directory.
func TestSeriesSurvivesExternalDirDeletion(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := SeriesKey{Name: "cpu"}
	now := time.Now()
	if err := s.Append(key, time.Second, now, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Simulate the series directory vanishing externally.
	sr := s.byKey[key.String()]
	if err := os.RemoveAll(sr.dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	results, err := s.QueryRange("cpu", nil, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange should tolerate a missing series dir, got error: %v", err)
	}
	if len(results) != 1 || len(results[0].Points) != 0 {
		t.Fatalf("expected 1 series with 0 points after dir deletion, got %+v", results)
	}

	// The next Append should self-heal: recreate the directory and succeed.
	// The in-memory open-chunk buffer survives the directory's deletion, so
	// both the pre-deletion and post-heal points come back once the chunk
	// is rewritten to the recreated directory.
	later := now.Add(time.Second)
	if err := s.Append(key, time.Second, later, 2); err != nil {
		t.Fatalf("Append after dir deletion should self-heal, got error: %v", err)
	}

	results, err = s.QueryRange("cpu", nil, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange after self-heal: %v", err)
	}
	if len(results) != 1 || len(results[0].Points) != 2 {
		t.Fatalf("expected the self-healed append to be queryable, got %+v", results)
	}
	if results[0].Points[0].Value != 1 || results[0].Points[1].Value != 2 {
		t.Errorf("expected both pre- and post-deletion points to survive, got %+v", results[0].Points)
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open should create nested dir: %v", err)
	}
	s.Close()
}
