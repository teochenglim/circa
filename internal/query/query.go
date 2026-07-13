// Package query is the only reader of internal/storage — the UI (once it
// exists) and any external tool go through here, never straight to storage,
// per ARCHITECTURE.md's ingestion-event walkthrough.
package query

import (
	"time"

	"github.com/teochenglim/circa/internal/storage"
)

// Result is one series' points over the requested range.
type Result struct {
	Metric map[string]string
	Points []storage.Point
}

type Engine struct {
	store *storage.Store
}

func New(store *storage.Store) *Engine {
	return &Engine{store: store}
}

// Range asks for every point with start <= t <= end.
type Range struct {
	Start time.Time
	End   time.Time
}

// QueryRange returns every series named name whose labels are a superset of
// match, each with its points restricted to r. match may be nil/empty to
// select every series with that name.
func (e *Engine) QueryRange(name string, match map[string]string, r Range) ([]Result, error) {
	series, err := e.store.QueryRange(name, match, r.Start, r.End)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(series))
	for _, sr := range series {
		metric := make(map[string]string, len(sr.Key.Labels)+1)
		for k, v := range sr.Key.Labels {
			metric[k] = v
		}
		metric["__name__"] = sr.Key.Name
		results = append(results, Result{Metric: metric, Points: sr.Points})
	}
	return results, nil
}
