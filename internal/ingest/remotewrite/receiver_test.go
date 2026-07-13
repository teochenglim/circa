package remotewrite

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"

	"github.com/teochenglim/circa/internal/ingest"
)

func TestReceiveHandlerDecodesSamples(t *testing.T) {
	req := &prompb.WriteRequest{
		Timeseries: []prompb.TimeSeries{
			{
				Labels: []prompb.Label{
					{Name: "__name__", Value: "cpu_usage"},
					{Name: "host", Value: "node1"},
				},
				Samples: []prompb.Sample{
					{Value: 42.5, Timestamp: 1700000000000},
				},
			},
		},
	}
	data, err := req.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	compressed := snappy.Encode(nil, data)

	var got []ingest.Sample
	handler := ReceiveHandler(func(s ingest.Sample) {
		got = append(got, s)
	}, nil)

	httpReq := httptest.NewRequest("POST", "/api/v1/write", bytes.NewReader(compressed))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httpReq)

	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204, body: %s", rec.Code, rec.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("got %d samples, want 1", len(got))
	}
	s := got[0]
	if s.Name != "cpu_usage" {
		t.Errorf("Name = %q, want cpu_usage", s.Name)
	}
	if s.Labels["host"] != "node1" {
		t.Errorf("Labels[host] = %q, want node1", s.Labels["host"])
	}
	if s.Value != 42.5 {
		t.Errorf("Value = %v, want 42.5", s.Value)
	}
	wantTime := time.UnixMilli(1700000000000)
	if !s.Time.Equal(wantTime) {
		t.Errorf("Time = %v, want %v", s.Time, wantTime)
	}
}

func TestReceiveHandlerRejectsBadBody(t *testing.T) {
	handler := ReceiveHandler(func(ingest.Sample) {
		t.Fatal("handler should not be called for garbage input")
	}, nil)

	httpReq := httptest.NewRequest("POST", "/api/v1/write", bytes.NewReader([]byte("not snappy")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httpReq)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
