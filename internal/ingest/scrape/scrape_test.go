package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

const exposition = `
# HELP node_cpu_seconds_total Seconds the CPU spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 12345.6

# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 42

# HELP http_request_duration_seconds A summary of request durations.
# TYPE http_request_duration_seconds summary
http_request_duration_seconds{quantile="0.5"} 0.02
http_request_duration_seconds{quantile="0.9"} 0.05
http_request_duration_seconds_sum 1.5
http_request_duration_seconds_count 100

# HELP http_request_size_bytes A histogram of request sizes.
# TYPE http_request_size_bytes histogram
http_request_size_bytes_bucket{le="100"} 5
http_request_size_bytes_bucket{le="1000"} 20
http_request_size_bytes_bucket{le="+Inf"} 25
http_request_size_bytes_sum 12345
http_request_size_bytes_count 25
`

func TestFetchDecodesAllMetricTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(exposition))
	}))
	defer srv.Close()

	target := Target{URL: srv.URL, Interval: 15 * time.Second, Labels: map[string]string{"job": "node"}}
	s := New(nil, nil, nil)
	samples, err := s.fetch(context.Background(), target)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	byName := map[string][]ingest.Sample{}
	for _, sm := range samples {
		byName[sm.Name] = append(byName[sm.Name], sm)
	}

	if got := byName["node_cpu_seconds_total"]; len(got) != 1 {
		t.Fatalf("node_cpu_seconds_total: got %d samples, want 1", len(got))
	} else {
		if got[0].Value != 12345.6 {
			t.Errorf("counter value = %v, want 12345.6", got[0].Value)
		}
		if got[0].Labels["job"] != "node" || got[0].Labels["cpu"] != "0" || got[0].Labels["mode"] != "idle" {
			t.Errorf("labels = %v", got[0].Labels)
		}
	}

	if got := byName["go_goroutines"]; len(got) != 1 || got[0].Value != 42 {
		t.Errorf("go_goroutines = %v, want [42]", got)
	}

	if got := byName["http_request_duration_seconds"]; len(got) != 2 {
		t.Errorf("summary quantiles: got %d, want 2", len(got))
	}
	if got := byName["http_request_duration_seconds_sum"]; len(got) != 1 || got[0].Value != 1.5 {
		t.Errorf("summary sum = %v, want [1.5]", got)
	}
	if got := byName["http_request_duration_seconds_count"]; len(got) != 1 || got[0].Value != 100 {
		t.Errorf("summary count = %v, want [100]", got)
	}

	if got := byName["http_request_size_bytes_bucket"]; len(got) != 3 {
		t.Errorf("histogram buckets: got %d, want 3", len(got))
	}
	if got := byName["http_request_size_bytes_sum"]; len(got) != 1 || got[0].Value != 12345 {
		t.Errorf("histogram sum = %v, want [12345]", got)
	}
}

func TestFetchNonOKStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := New(nil, nil, nil)
	_, err := s.fetch(context.Background(), Target{URL: srv.URL, Interval: time.Second})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestRunEmptyTargetsReturnsOnContextCancel(t *testing.T) {
	s := New(nil, func(ingest.Sample) {}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with no targets did not return after context cancellation")
	}
}

func TestRunScrapesOnInterval(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Write([]byte(`go_goroutines 1` + "\n"))
	}))
	defer srv.Close()

	var samplesMu sync.Mutex
	var received []ingest.Sample
	targets := []Target{{URL: srv.URL, Interval: 20 * time.Millisecond}}
	s := New(targets, func(sm ingest.Sample) {
		samplesMu.Lock()
		received = append(received, sm)
		samplesMu.Unlock()
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	s.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if hits < 2 {
		t.Errorf("expected at least 2 scrapes in 90ms at 20ms interval, got %d", hits)
	}
	samplesMu.Lock()
	defer samplesMu.Unlock()
	if len(received) < 2 {
		t.Errorf("expected at least 2 samples handled, got %d", len(received))
	}
}
