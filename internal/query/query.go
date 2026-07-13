// Package query is the only reader of internal/storage — the UI and any
// external tool go through here, never straight to storage, per
// ARCHITECTURE.md's ingestion-event walkthrough.
package query

import (
	"sort"
	"time"

	"github.com/teochenglim/circa/internal/storage"
)

// Result is one series' single-valued (tier-0/raw) points over the
// requested range.
type Result struct {
	Metric map[string]string
	Points []storage.Point
}

// AggResult is one series' min/avg/max rolled-up points (tier-1/tier-2) over
// the requested range.
type AggResult struct {
	Metric map[string]string
	Points []storage.AggPoint
}

type Engine struct {
	store *storage.TieredStore
}

func New(store *storage.TieredStore) *Engine {
	return &Engine{store: store}
}

// Range asks for every point with start <= t <= end.
type Range struct {
	Start time.Time
	End   time.Time
}

// QueryRange returns every series named name whose labels are a superset of
// match, each with its points restricted to r. match may be nil/empty to
// select every series with that name. tier picks the resolution: raw
// results are returned for storage.TierRaw, min/avg/max agg results for
// storage.TierMinute/TierHour.
func (e *Engine) QueryRange(name string, match map[string]string, tier storage.Tier, r Range) ([]Result, []AggResult, error) {
	rawSeries, aggSeries, err := e.store.QueryRange(name, match, tier, r.Start, r.End)
	if err != nil {
		return nil, nil, err
	}

	if tier == storage.TierRaw {
		results := make([]Result, 0, len(rawSeries))
		for _, sr := range rawSeries {
			results = append(results, Result{Metric: metricLabels(sr.Key), Points: sr.Points})
		}
		return results, nil, nil
	}

	results := make([]AggResult, 0, len(aggSeries))
	for _, sr := range aggSeries {
		results = append(results, AggResult{Metric: metricLabels(sr.Key), Points: sr.Points})
	}
	return nil, results, nil
}

// Series lists every real (non-rollup) series currently known to the store,
// for the UI's metric picker.
func (e *Engine) Series() []storage.SeriesKey {
	return e.store.Series()
}

// AnomalyRank is one series' anomaly rate over a recent window — the "what's
// unusual right now" ranked list DESIGN/06 §6.2 calls for, computed by
// reading the anomaly bit already embedded in raw storage (no separate
// aggregation subsystem needed).
type AnomalyRank struct {
	Metric map[string]string
	Rate   float64 // fraction of points in the window flagged anomalous, (0,1]
	Count  int     // points evaluated
}

// AnomalyRanking scores every known series' raw points over the last window
// (ending at now) and returns those with at least one anomalous point,
// ranked highest-rate first. Series never scored by internal/anomaly (or
// scored and found entirely normal) simply don't appear — an empty result
// isn't distinguishable from "features.ml is off," which is fine since both
// mean "nothing to show."
func (e *Engine) AnomalyRanking(window time.Duration, now time.Time) []AnomalyRank {
	keys := e.store.Series()
	ranks := make([]AnomalyRank, 0, len(keys))

	for _, key := range keys {
		results, _, err := e.QueryRange(key.Name, key.Labels, storage.TierRaw, Range{Start: now.Add(-window), End: now})
		if err != nil {
			continue
		}
		for _, res := range results {
			if len(res.Points) == 0 {
				continue
			}
			anomalous := 0
			for _, p := range res.Points {
				if p.Anomalous {
					anomalous++
				}
			}
			if anomalous == 0 {
				continue
			}
			ranks = append(ranks, AnomalyRank{
				Metric: res.Metric,
				Rate:   float64(anomalous) / float64(len(res.Points)),
				Count:  len(res.Points),
			})
		}
	}

	sort.Slice(ranks, func(i, j int) bool { return ranks[i].Rate > ranks[j].Rate })
	return ranks
}

func metricLabels(key storage.SeriesKey) map[string]string {
	metric := make(map[string]string, len(key.Labels)+1)
	for k, v := range key.Labels {
		metric[k] = v
	}
	metric["__name__"] = key.Name
	return metric
}
