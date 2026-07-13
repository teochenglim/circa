// Package anomaly implements circa's per-metric anomaly detection, closely
// following the real algorithm in Netdata's own ml/ (not just DESIGN/06's
// summary of it — see netdata/src/ml/{ml.cc,ml_kmeans.cc,ml_features.cc,
// ml_config.cc} for the reference implementation this was matched against,
// and DESIGN/10_ml_summary.md for the full mapping of what's replicated vs.
// deliberately deferred):
//
//   - Feature vectors are built by differencing (DiffN), smoothing (SmoothN,
//     a rolling mean), then lagging (LagN, an overlapping sliding window) —
//     see Preprocess, matching ml_features.cc exactly (order: diff, smooth,
//     lag; no normalization or abs()).
//   - Each model is unsupervised k-means, k=2, matching ml_kmeans.cc.
//   - A model's anomaly score for a vector is 100 * the min-max-normalized
//     mean distance to both centroids (not nearest-centroid distance),
//     matching ml_kmeans_anomaly_score exactly, clamped to [0,100].
//   - An ensemble of ModelCount models (Netdata's default: 18) is accumulated
//     over time via detector.go's FIFO retraining, not all trained on the
//     same recent window — see detector.go's doc comment.
package anomaly

import (
	"errors"
	"math"
)

// maxIterations bounds k-means' Lloyd's-algorithm loop, matching the
// (clamped) range of Netdata's own "maximum number of k-means iterations"
// (default 1000, clamped 500-1000). k=2 on small feature vectors converges
// in far fewer than this in practice; a fixed cap keeps Train's cost
// predictable without needing a convergence check.
const maxIterations = 500

// Model is one trained k-means(k=2) model: two centroids plus the min/max
// mean-distance observed across the training data, used to min-max-
// normalize a new point's score into [0,100] (ml_kmeans.cc's
// ml_kmeans_anomaly_score).
type Model struct {
	Centroids [2][]float64
	MinDist   float64
	MaxDist   float64
}

// Train fits a k-means(k=2) model to vectors (each must be the same
// length — build them with Preprocess) and records the min/max mean-
// distance-to-both-centroids seen across the training data.
func Train(vectors [][]float64) (*Model, error) {
	if len(vectors) < 2 {
		return nil, errors.New("anomaly: need at least 2 feature vectors to train")
	}

	c0, c1 := initCentroids(vectors)
	assign := make([]int, len(vectors))
	for iter := 0; iter < maxIterations; iter++ {
		for i, v := range vectors {
			if euclidean(v, c0) <= euclidean(v, c1) {
				assign[i] = 0
			} else {
				assign[i] = 1
			}
		}
		c0 = mean(vectors, assign, 0, c0)
		c1 = mean(vectors, assign, 1, c1)
	}

	m := &Model{Centroids: [2][]float64{c0, c1}, MinDist: math.MaxFloat64, MaxDist: -math.MaxFloat64}
	for _, v := range vectors {
		d := meanDistance(v, c0, c1)
		if d < m.MinDist {
			m.MinDist = d
		}
		if d > m.MaxDist {
			m.MaxDist = d
		}
	}
	return m, nil
}

// Score is v's anomaly score in [0,100]: the min-max-normalized mean
// distance to both centroids, exactly matching ml_kmeans_anomaly_score.
// Returns 0 (never anomalous) when MinDist == MaxDist — a perfectly
// constant training set has no distance range to normalize against, so
// Netdata treats it as "nothing to compare to" rather than flagging any
// deviation at all (the degenerate case a naive percentile-threshold
// approach mishandles).
func (m *Model) Score(v []float64) float64 {
	if m.MaxDist == m.MinDist {
		return 0
	}
	d := meanDistance(v, m.Centroids[0], m.Centroids[1])
	score := 100 * math.Abs((d-m.MinDist)/(m.MaxDist-m.MinDist))
	if score > 100 {
		score = 100
	}
	return score
}

// IsAnomalous reports whether v's score meets or exceeds thresholdPercent
// (0..100) — the per-model half of DESIGN/06 §6.2's "distance exceeds a
// threshold based on the 99th percentile of training data."
func (m *Model) IsAnomalous(v []float64, thresholdPercent float64) bool {
	return m.Score(v) >= thresholdPercent
}

func meanDistance(v, c0, c1 []float64) float64 {
	return (euclidean(v, c0) + euclidean(v, c1)) / 2
}

// initCentroids picks two starting centroids deterministically (a cheap
// k-means++-style spread rather than random init, so results are
// reproducible given the same training data): the first vector, and
// whichever vector is farthest from it.
func initCentroids(vectors [][]float64) ([]float64, []float64) {
	c0 := vectors[0]
	var c1 []float64
	best := -1.0
	for _, v := range vectors {
		d := euclidean(v, c0)
		if d > best {
			best = d
			c1 = v
		}
	}
	if c1 == nil {
		c1 = vectors[0]
	}
	return append([]float64(nil), c0...), append([]float64(nil), c1...)
}

// mean returns the centroid of every vector assigned to cluster, or the
// previous centroid unchanged if nothing is currently assigned to it
// (avoids a division by zero collapsing an empty cluster to NaN).
func mean(vectors [][]float64, assign []int, cluster int, previous []float64) []float64 {
	sum := make([]float64, len(previous))
	n := 0
	for i, v := range vectors {
		if assign[i] != cluster {
			continue
		}
		for j, x := range v {
			sum[j] += x
		}
		n++
	}
	if n == 0 {
		return previous
	}
	for j := range sum {
		sum[j] /= float64(n)
	}
	return sum
}

func euclidean(a, b []float64) float64 {
	sum := 0.0
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum)
}

// Preprocess turns a raw value series into feature vectors, in the exact
// order ml_features.cc uses: difference, then smooth, then lag. Returns nil
// if values is too short to produce even one vector (needs at least
// diffN+effectiveSmoothN+lagN+1 raw values).
func Preprocess(values []float64, diffN, smoothN, lagN int) [][]float64 {
	return SlidingWindows(movingAverage(diff(values, diffN), smoothN), lagN+1)
}

// diff computes the order-n backward difference: result[i] = values[i+n] -
// values[i]. n == 0 returns values unchanged (copied) — matches
// ml_features_diff, which only ever runs with n a clamped 0 or 1 in
// Netdata's own config but is implemented generally here.
func diff(values []float64, n int) []float64 {
	if n <= 0 {
		return append([]float64(nil), values...)
	}
	if len(values) <= n {
		return nil
	}
	out := make([]float64, len(values)-n)
	for i := range out {
		out[i] = values[i+n] - values[i]
	}
	return out
}

// movingAverage computes a simple rolling mean with window n. n <= 1 is a
// no-op (matches ml_features.cc's ml_effective_smooth_n treating 0 as an
// effective window of 1).
func movingAverage(values []float64, n int) []float64 {
	if n <= 1 {
		return values
	}
	if len(values) < n {
		return nil
	}
	out := make([]float64, len(values)-n+1)
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += values[i]
	}
	out[0] = sum / float64(n)
	for i := n; i < len(values); i++ {
		sum += values[i] - values[i-n]
		out[i-n+1] = sum / float64(n)
	}
	return out
}

// SlidingWindows extracts every contiguous window-length window from values
// as a feature vector — Preprocess's final "lag" step.
func SlidingWindows(values []float64, window int) [][]float64 {
	if window < 1 || len(values) < window {
		return nil
	}
	out := make([][]float64, 0, len(values)-window+1)
	for i := 0; i+window <= len(values); i++ {
		v := make([]float64, window)
		copy(v, values[i:i+window])
		out = append(out, v)
	}
	return out
}
