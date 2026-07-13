package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

func newTestEngine(t *testing.T) *query.Engine {
	t.Helper()
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return query.New(store)
}

func TestHealthz(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestQueryRangeMissingMetric(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?start=0&end=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestQueryRangeReturnsIngestedPoints(t *testing.T) {
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	now := time.Now()
	key := storage.SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}
	if err := store.Raw.Append(key, time.Second, now, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	router := NewRouter(query.New(store), Options{})
	url := "/api/v1/query_range?metric=up&labels=job=node&start=" +
		strconv.FormatInt(now.Add(-time.Minute).Unix(), 10) +
		"&end=" + strconv.FormatInt(now.Add(time.Minute).Unix(), 10)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("status = %q, want success", resp.Status)
	}
	if len(resp.Data.Result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(resp.Data.Result))
	}
	if resp.Data.Result[0].Metric["__name__"] != "up" {
		t.Errorf("metric = %+v", resp.Data.Result[0].Metric)
	}
	if len(resp.Data.Result[0].Values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(resp.Data.Result[0].Values))
	}
}

func TestQueryRangeInvalidTimeRange(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?metric=up&start=100&end=50", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestQueryRangeInvalidTier(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?metric=up&start=0&end=1&tier=bogus", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestQueryRangeMinuteTierReturnsMinAvgMax(t *testing.T) {
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, time.Hour, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	base := time.Unix(0, 0).Truncate(time.Minute)
	router := NewRouter(query.New(store), Options{})

	for _, tc := range []struct {
		offsetSec int
		value     float64
	}{{0, 10}, {15, 20}, {30, 30}, {60, 99}} {
		if err := store.Consume(sampleAt("cpu", base.Add(time.Duration(tc.offsetSec)*time.Second), tc.value)); err != nil {
			t.Fatalf("Consume: %v", err)
		}
	}

	url := "/api/v1/query_range?metric=cpu&tier=minute&start=" +
		strconv.FormatInt(base.Add(-time.Hour).Unix(), 10) +
		"&end=" + strconv.FormatInt(base.Add(time.Hour).Unix(), 10)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp apiResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Data.Result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(resp.Data.Result))
	}
	series := resp.Data.Result[0]
	if len(series.Values) != 1 || len(series.Min) != 1 || len(series.Max) != 1 {
		t.Fatalf("expected 1 flushed bucket with min/avg/max, got values=%v min=%v max=%v", series.Values, series.Min, series.Max)
	}
	if series.Values[0][1] != "20" || series.Min[0][1] != "10" || series.Max[0][1] != "30" {
		t.Errorf("bucket = values=%v min=%v max=%v, want avg=20 min=10 max=30", series.Values[0], series.Min[0], series.Max[0])
	}
}

func TestSeriesEndpointListsIngestedSeries(t *testing.T) {
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	if err := store.Raw.Append(storage.SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}, time.Second, time.Now(), 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	router := NewRouter(query.New(store), Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/series", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Name != "up" || resp.Data[0].Labels["job"] != "node" {
		t.Errorf("data = %+v", resp.Data)
	}
}

func TestIndexPageServesDashboard(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func sampleAt(name string, t time.Time, value float64) ingest.Sample {
	return ingest.Sample{Name: name, Time: t, Value: value, Interval: 15 * time.Second}
}
