package query

import (
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/storage"
)

func TestQueryRangeReturnsMetricLabelsIncludingName(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	now := time.Now()
	key := storage.SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}
	if err := store.Append(key, time.Second, now, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	e := New(store)
	results, err := e.QueryRange("up", nil, Range{Start: now.Add(-time.Minute), End: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
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
	store, err := storage.Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	e := New(store)
	results, err := e.QueryRange("nonexistent", nil, Range{Start: time.Now().Add(-time.Hour), End: time.Now()})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}
