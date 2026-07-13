package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

func TestMetricsEndpointServesExpositionFormat(t *testing.T) {
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, 0, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	now := time.Now()
	key := storage.SeriesKey{Name: "up", Labels: map[string]string{"job": "node"}}
	if err := store.Raw.Append(key, time.Second, now, 1, false); err != nil {
		t.Fatalf("Append: %v", err)
	}

	router := NewRouter(query.New(store), Options{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `up{job="node"} 1`) {
		t.Errorf("body missing expected exposition line, got: %s", body)
	}
}

// TestMetricsEndpointSelfMetricsPresentWhenNothingIngested confirms
// /metrics still renders circa's own RED self-metrics (selfmetrics.go) even
// with nothing in storage — the endpoint is never truly empty once circa's
// own request has been recorded by instrumentHTTP, unlike the pre-v0.7.0
// behavior of an ingested-data-only endpoint.
func TestMetricsEndpointSelfMetricsPresentWhenNothingIngested(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{})

	// instrumentHTTP records a request's own self-metrics only *after* its
	// handler returns, so a request never sees itself reflected in its own
	// /metrics response — make one throwaway request first so there's at
	// least one recorded observation to gather, independent of whatever
	// other tests in this package happened to run earlier.
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "circa_http_requests_total") {
		t.Errorf("body missing self-metrics, got: %s", body)
	}
}
