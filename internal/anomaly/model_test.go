package anomaly

import (
	"math"
	"testing"
)

const threshold = 99.0 // matches DefaultScoreThreshold

func TestTrainSeparatesTwoClusters(t *testing.T) {
	// Real training data always has some noise; a perfectly noiseless
	// cluster (variance 0) is a degenerate case covered separately by
	// TestTrainOnConstantSeriesNeverFlagsAnomalous; here jitter each vector
	// by a tiny amount so MinDist/MaxDist give a real normalization range.
	var vectors [][]float64
	jitter := []float64{0, 0.1, -0.1, 0.2, -0.2}
	for i := 0; i < 20; i++ {
		j := jitter[i%len(jitter)]
		vectors = append(vectors, []float64{j, j, j})
	}
	for i := 0; i < 20; i++ {
		j := jitter[i%len(jitter)]
		vectors = append(vectors, []float64{100 + j, 100 + j, 100 + j})
	}

	model, err := Train(vectors)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}

	// One centroid should land near each cluster.
	near0 := math.Min(euclidean(model.Centroids[0], []float64{0, 0, 0}), euclidean(model.Centroids[1], []float64{0, 0, 0}))
	near100 := math.Min(euclidean(model.Centroids[0], []float64{100, 100, 100}), euclidean(model.Centroids[1], []float64{100, 100, 100}))
	if near0 > 1 {
		t.Errorf("no centroid near the {0,0,0} cluster: centroids=%v", model.Centroids)
	}
	if near100 > 1 {
		t.Errorf("no centroid near the {100,100,100} cluster: centroids=%v", model.Centroids)
	}

	// A point within the training data's own noise band should not be
	// anomalous — it must stay inside the ~0.2 jitter magnitude above.
	if model.IsAnomalous([]float64{0.15, 0.15, 0.15}, threshold) {
		t.Error("point within cluster 0's noise band flagged anomalous")
	}
	if model.IsAnomalous([]float64{99.85, 99.85, 99.85}, threshold) {
		t.Error("point within cluster 1's noise band flagged anomalous")
	}
	// A point far from both clusters should be anomalous.
	if !model.IsAnomalous([]float64{500, 500, 500}, threshold) {
		t.Error("point far from both clusters not flagged anomalous")
	}
}

func TestTrainOnConstantSeriesNeverFlagsAnomalous(t *testing.T) {
	// Matches ml_kmeans.cc exactly: MinDist == MaxDist (no variance to
	// normalize against) means Score always returns 0, so nothing is ever
	// flagged - not "everything is anomalous," which a naive
	// distance-threshold approach would produce instead.
	var vectors [][]float64
	for i := 0; i < 30; i++ {
		vectors = append(vectors, []float64{50, 50, 50, 50})
	}
	model, err := Train(vectors)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if model.MinDist != model.MaxDist {
		t.Fatalf("expected MinDist == MaxDist on constant training data, got %v/%v", model.MinDist, model.MaxDist)
	}
	if model.IsAnomalous([]float64{50, 50, 50, 50}, threshold) {
		t.Error("exact training value flagged anomalous")
	}
	if model.IsAnomalous([]float64{50, 50, 50, 5000}, threshold) {
		t.Error("even a large spike must not be flagged anomalous when MinDist == MaxDist")
	}
	if got := model.Score([]float64{50, 50, 50, 5000}); got != 0 {
		t.Errorf("Score = %v, want 0", got)
	}
}

func TestScoreIsClampedTo100(t *testing.T) {
	var vectors [][]float64
	jitter := []float64{0, 1, -1}
	for i := 0; i < 10; i++ {
		j := jitter[i%len(jitter)]
		vectors = append(vectors, []float64{j})
	}
	model, err := Train(vectors)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if got := model.Score([]float64{1e9}); got != 100 {
		t.Errorf("Score for an extreme outlier = %v, want 100 (clamped)", got)
	}
}

func TestTrainRejectsTooFewVectors(t *testing.T) {
	if _, err := Train([][]float64{{1, 2}}); err == nil {
		t.Error("expected error training on fewer than 2 vectors")
	}
	if _, err := Train(nil); err == nil {
		t.Error("expected error training on no vectors")
	}
}

func TestDiff(t *testing.T) {
	if got := diff([]float64{1, 3, 6, 10}, 1); !equalSlice(got, []float64{2, 3, 4}) {
		t.Errorf("diff order 1 = %v", got)
	}
	if got := diff([]float64{1, 3, 6}, 0); !equalSlice(got, []float64{1, 3, 6}) {
		t.Errorf("diff order 0 should be a no-op copy, got %v", got)
	}
	if got := diff([]float64{1}, 1); got != nil {
		t.Errorf("diff order 1 on a single value should be nil, got %v", got)
	}
}

func TestMovingAverage(t *testing.T) {
	if got := movingAverage([]float64{1, 2, 3, 4}, 2); !equalSlice(got, []float64{1.5, 2.5, 3.5}) {
		t.Errorf("movingAverage window 2 = %v", got)
	}
	if got := movingAverage([]float64{1, 2, 3}, 1); !equalSlice(got, []float64{1, 2, 3}) {
		t.Errorf("movingAverage window 1 should be a no-op, got %v", got)
	}
	if got := movingAverage([]float64{1, 2, 3}, 0); !equalSlice(got, []float64{1, 2, 3}) {
		t.Errorf("movingAverage window 0 should be a no-op, got %v", got)
	}
}

func TestPreprocessMatchesDiffSmoothLagOrder(t *testing.T) {
	// diff order 1 of 1..10 -> nine 1's; smoothing a run of identical values
	// is a no-op; lag_n=2 gives vectors of length 3 from the resulting run.
	values := make([]float64, 10)
	for i := range values {
		values[i] = float64(i + 1)
	}
	vectors := Preprocess(values, 1, 3, 2)
	if len(vectors) == 0 {
		t.Fatal("expected at least one feature vector")
	}
	for _, v := range vectors {
		for _, x := range v {
			if x != 1 {
				t.Errorf("vector = %v, want all 1's (constant first difference of a linear ramp)", v)
			}
		}
		if len(v) != 3 {
			t.Errorf("vector length = %d, want lag_n+1 = 3", len(v))
		}
	}
}

func TestPreprocessTooShortReturnsNil(t *testing.T) {
	if got := Preprocess([]float64{1, 2}, 1, 3, 5); got != nil {
		t.Errorf("expected nil for too-short input, got %v", got)
	}
}

func TestSlidingWindows(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	windows := SlidingWindows(values, 3)
	want := [][]float64{{1, 2, 3}, {2, 3, 4}, {3, 4, 5}}
	if len(windows) != len(want) {
		t.Fatalf("got %d windows, want %d", len(windows), len(want))
	}
	for i := range want {
		for j := range want[i] {
			if windows[i][j] != want[i][j] {
				t.Errorf("window %d = %v, want %v", i, windows[i], want[i])
			}
		}
	}
}

func TestSlidingWindowsTooShort(t *testing.T) {
	if got := SlidingWindows([]float64{1, 2}, 5); got != nil {
		t.Errorf("expected nil for values shorter than window, got %v", got)
	}
	if got := SlidingWindows([]float64{1, 2, 3}, 0); got != nil {
		t.Errorf("expected nil for window < 1, got %v", got)
	}
}

func equalSlice(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
