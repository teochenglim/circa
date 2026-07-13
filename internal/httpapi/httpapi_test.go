package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

func newTestEngine(t *testing.T) *query.Engine {
	t.Helper()
	store, err := storage.Open(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return query.New(store)
}

func TestHealthz(t *testing.T) {
	router := NewRouter(newTestEngine(t))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestQueryRangeMissingMetric(t *testing.T) {
	router := NewRouter(newTestEngine(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?start=0&end=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestQueryRangeReturnsIngestedPoints(t *testing.T) {
	store, err := storage.Open(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	now := time.Now()
	key := storage.SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}
	if err := store.Append(key, time.Second, now, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	router := NewRouter(query.New(store))
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
	router := NewRouter(newTestEngine(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?metric=up&start=100&end=50", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
