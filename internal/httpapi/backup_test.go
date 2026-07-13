package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/backup"
	"github.com/teochenglim/circa/internal/config"
	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

func TestBackupRangeNotRegisteredWhenDisabled(t *testing.T) {
	router := NewRouter(newTestEngine(t), Options{}) // features.backup defaults false
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup_range", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route shouldn't exist)", rec.Code)
	}
}

func TestBackupRangeNotRegisteredInPushMode(t *testing.T) {
	cfg := config.Config{Features: config.Features{Backup: true}, Backup: config.Backup{Mode: "push"}}
	router := NewRouter(newTestEngine(t), Options{Config: cfg})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup_range", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (push mode never serves this)", rec.Code)
	}
}

func TestBackupRangeReturnsDeltaSinceWatermark(t *testing.T) {
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, time.Hour, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	interval := 15 * time.Second
	base := time.Unix(0, 0).Truncate(time.Minute)
	key := storage.SeriesKey{Name: "cpu", Labels: map[string]string{"host": "a"}}
	for i, v := range []float64{10, 20, 30, 40, 1, 2, 3, 4, 99} {
		store.Consume(ingest.Sample{
			Name: key.Name, Labels: key.Labels,
			Time: base.Add(time.Duration(i) * interval), Value: v, Interval: interval,
		})
	}

	cfg := config.Config{Features: config.Features{Backup: true}, Backup: config.Backup{Mode: "pull", NodeID: "node-1"}}
	router := NewRouter(query.New(store), Options{Config: cfg, NodeID: "node-1", Hostname: "host-a"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup_range?since="+
		formatUnix(base), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var out backup.DeltaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("got %d rows, want 1 (bucket at `since` excluded): %+v", len(out.Rows), out.Rows)
	}
	if out.Rows[0].NodeID != "node-1" || out.Rows[0].Hostname != "host-a" {
		t.Errorf("row identity = %+v", out.Rows[0])
	}
	if out.Rows[0].Value != 2.5 {
		t.Errorf("row value = %v, want 2.5", out.Rows[0].Value)
	}
}

func TestBackupRangeEmptySinceMeansFromBeginning(t *testing.T) {
	store, err := storage.OpenTiered(t.TempDir(), time.Hour, time.Hour, 0)
	if err != nil {
		t.Fatalf("storage.OpenTiered: %v", err)
	}
	defer store.Close()

	interval := 15 * time.Second
	base := time.Unix(0, 0).Truncate(time.Minute)
	key := storage.SeriesKey{Name: "cpu", Labels: nil}
	for i, v := range []float64{10, 20, 30, 40, 99} {
		store.Consume(ingest.Sample{
			Name: key.Name, Time: base.Add(time.Duration(i) * interval), Value: v, Interval: interval,
		})
	}

	cfg := config.Config{Features: config.Features{Backup: true}, Backup: config.Backup{Mode: "pull"}}
	router := NewRouter(query.New(store), Options{Config: cfg})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup_range", nil) // no ?since at all
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out backup.DeltaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("got %d rows, want 1 (the one flushed bucket): %+v", len(out.Rows), out.Rows)
	}
}

func formatUnix(t time.Time) string {
	return time.Unix(t.Unix(), 0).UTC().Format("2006-01-02T15:04:05Z")
}
