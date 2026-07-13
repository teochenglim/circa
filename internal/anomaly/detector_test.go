package anomaly

import (
	"context"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

// fakeReader serves a fixed set of raw points for every series, regardless
// of the requested range — enough for retrainOne to build feature vectors.
type fakeReader struct {
	keys   []storage.SeriesKey
	points map[string][]storage.Point // keyed by SeriesKey.String()
}

func (f *fakeReader) Series() []storage.SeriesKey { return f.keys }

func (f *fakeReader) QueryRange(name string, match map[string]string, tier storage.Tier, r query.Range) ([]query.Result, []query.AggResult, error) {
	for _, k := range f.keys {
		if k.Name != name {
			continue
		}
		return []query.Result{{Metric: map[string]string{"__name__": name}, Points: f.points[k.String()]}}, nil, nil
	}
	return nil, nil, nil
}

func constantPoints(n int, value float64) []storage.Point {
	points := make([]storage.Point, n)
	base := time.Now().Add(-time.Duration(n) * time.Second)
	for i := range points {
		points[i] = storage.Point{Time: base.Add(time.Duration(i) * time.Second), Value: value}
	}
	return points
}

// jitteredPoints is constantPoints with a tiny amount of noise — a
// perfectly noiseless training set is a real but degenerate case (Model's
// MinDist == MaxDist, so it never flags anything anomalous — see
// model_test.go's TestTrainOnConstantSeriesNeverFlagsAnomalous); tests that
// want to see a spike actually get flagged need a nonzero normalization
// range, same as real-world data always has.
func jitteredPoints(n int, value float64) []storage.Point {
	points := constantPoints(n, value)
	jitter := []float64{0, 0.1, -0.1, 0.2, -0.2}
	for i := range points {
		points[i].Value += jitter[i%len(jitter)]
	}
	return points
}

// noPreprocessConfig disables differencing and smoothing (DiffN=0, SmoothN=0
// -> effective window 1) so the raw-value buffer length is exactly
// LagN+1 — keeps these tests' arithmetic simple and focused on Detector's
// own buffering/ensemble logic rather than Preprocess's math (covered
// separately in model_test.go).
func noPreprocessConfig(modelCount, lagN int, trainingWindow, retrainInterval time.Duration) Config {
	return Config{
		ModelCount:      modelCount,
		TrainingWindow:  trainingWindow,
		RetrainInterval: retrainInterval,
		DiffN:           0,
		SmoothN:         0,
		LagN:            lagN,
		ScoreThreshold:  threshold,
	}
}

func TestScoreBeforeEnoughHistoryReturnsFalse(t *testing.T) {
	cfg := noPreprocessConfig(3, 5, time.Hour, time.Hour) // raw buffer needs LagN+1 = 6
	d := New(cfg, &fakeReader{}, nil)
	key := storage.SeriesKey{Name: "cpu"}
	for i := 0; i < 5; i++ { // fewer than the 6 needed
		if d.Score(key, float64(i)) {
			t.Fatal("Score should be false before the raw buffer fills")
		}
	}
}

func TestScoreBeforeAnyModelTrainedReturnsFalse(t *testing.T) {
	cfg := noPreprocessConfig(3, 2, time.Hour, time.Hour)
	d := New(cfg, &fakeReader{}, nil)
	key := storage.SeriesKey{Name: "cpu"}
	var got bool
	for i := 0; i < 10; i++ {
		got = d.Score(key, 1)
	}
	if got {
		t.Fatal("Score should be false when no model has trained yet, however full the buffer is")
	}
}

func TestRetrainThenScoreDetectsSpike(t *testing.T) {
	key := storage.SeriesKey{Name: "cpu"}
	reader := &fakeReader{
		keys:   []storage.SeriesKey{key},
		points: map[string][]storage.Point{key.String(): jitteredPoints(50, 10)},
	}
	cfg := noPreprocessConfig(1, 3, time.Hour, time.Hour) // raw buffer needs 4
	d := New(cfg, reader, nil)

	d.retrainOne(key, time.Now())

	// Feed normal values within the training data's own noise band - should
	// not be anomalous.
	var lastNormal bool
	for i := 0; i < 4; i++ {
		lastNormal = d.Score(key, 10.1)
	}
	if lastNormal {
		t.Error("value within the training noise band flagged anomalous")
	}

	// Feed a wild spike, enough times to push it fully into the feature window.
	var lastSpike bool
	for i := 0; i < 4; i++ {
		lastSpike = d.Score(key, 100000)
	}
	if !lastSpike {
		t.Error("extreme spike should be flagged anomalous after training on constant data")
	}
}

func TestRetrainSkipsSeriesWithTooLittleHistory(t *testing.T) {
	key := storage.SeriesKey{Name: "cpu"}
	reader := &fakeReader{
		keys:   []storage.SeriesKey{key},
		points: map[string][]storage.Point{key.String(): constantPoints(3, 10)}, // too few for lag_n=6 (needs 7+)
	}
	cfg := noPreprocessConfig(1, 6, time.Hour, time.Hour)
	d := New(cfg, reader, nil)
	d.retrainOne(key, time.Now())

	e := d.ensembleFor(key)
	if len(e.models) != 0 {
		t.Errorf("expected no model trained with insufficient history, got %d model slots", len(e.models))
	}
}

func TestRetrainAppendsAndEvictsOldestOnceAtCapacity(t *testing.T) {
	// Netdata fidelity check (DESIGN/10_ml_summary.md §3): retraining
	// accumulates a FIFO of up to ModelCount models rather than overwriting
	// a fixed slot — each retrainOne call should grow the ensemble until
	// ModelCount, then hold steady at that size (oldest evicted, not the
	// newest rejected).
	key := storage.SeriesKey{Name: "cpu"}
	reader := &fakeReader{
		keys:   []storage.SeriesKey{key},
		points: map[string][]storage.Point{key.String(): constantPoints(50, 10)},
	}
	cfg := noPreprocessConfig(3, 3, time.Hour, time.Hour)
	d := New(cfg, reader, nil)

	e := d.ensembleFor(key)
	for i := 0; i < 5; i++ {
		d.retrainOne(key, time.Now())
		e.mu.Lock()
		n := len(e.models)
		e.mu.Unlock()
		want := i + 1
		if want > cfg.ModelCount {
			want = cfg.ModelCount
		}
		if n != want {
			t.Errorf("after %d retrains: len(models) = %d, want %d", i+1, n, want)
		}
	}
}

func TestRunRetrainsOnTick(t *testing.T) {
	key := storage.SeriesKey{Name: "cpu"}
	reader := &fakeReader{
		keys:   []storage.SeriesKey{key},
		points: map[string][]storage.Point{key.String(): constantPoints(50, 10)},
	}
	cfg := noPreprocessConfig(1, 3, time.Hour, 20*time.Millisecond)
	d := New(cfg, reader, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	d.Run(ctx)

	e := d.ensembleFor(key)
	e.mu.Lock()
	trained := len(e.models) > 0
	e.mu.Unlock()
	if !trained {
		t.Error("expected Run to have retrained at least once within the timeout")
	}
}

func TestDetectorZeroRetrainIntervalRunNoop(t *testing.T) {
	d := New(Config{}, &fakeReader{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	d.Run(ctx) // must return promptly, not hang or panic
}
