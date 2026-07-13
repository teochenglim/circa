// Package query is the only reader of internal/storage — the UI and any
// external tool go through here, never straight to storage, per
// ARCHITECTURE.md's ingestion-event walkthrough.
package query

import (
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

func metricLabels(key storage.SeriesKey) map[string]string {
	metric := make(map[string]string, len(key.Labels)+1)
	for k, v := range key.Labels {
		metric[k] = v
	}
	metric["__name__"] = key.Name
	return metric
}
