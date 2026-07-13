package remotewrite

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

type fakeReader struct {
	keys    []storage.SeriesKey
	results []query.Result
}

func (f *fakeReader) Series() []storage.SeriesKey { return f.keys }

func (f *fakeReader) QueryRange(name string, match map[string]string, tier storage.Tier, r query.Range) ([]query.Result, []query.AggResult, error) {
	return f.results, nil, nil
}

func TestSenderPushesKnownSeries(t *testing.T) {
	var receivedTS []prompb.TimeSeries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
			return
		}
		data, err := snappy.Decode(nil, body)
		if err != nil {
			t.Errorf("snappy decode: %v", err)
			return
		}
		var req prompb.WriteRequest
		if err := req.Unmarshal(data); err != nil {
			t.Errorf("protobuf decode: %v", err)
			return
		}
		receivedTS = append(receivedTS, req.Timeseries...)
		w.WriteHeader(204)
	}))
	defer server.Close()

	key := storage.SeriesKey{Name: "cpu_usage", Labels: map[string]string{"host": "node1"}}
	reader := &fakeReader{
		keys: []storage.SeriesKey{key},
		results: []query.Result{
			{
				Metric: map[string]string{"__name__": "cpu_usage", "host": "node1"},
				Points: []storage.Point{{Time: time.Now(), Value: 3.14}},
			},
		},
	}

	sender := NewSender(server.URL, reader, nil)
	if err := sender.sendOnce(t.Context(), 30*time.Second); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}

	if len(receivedTS) != 1 {
		t.Fatalf("received %d timeseries, want 1", len(receivedTS))
	}
	if len(receivedTS[0].Samples) != 1 || receivedTS[0].Samples[0].Value != 3.14 {
		t.Errorf("unexpected samples: %+v", receivedTS[0].Samples)
	}
}

func TestSenderSkipsEmptySeries(t *testing.T) {
	reader := &fakeReader{
		keys:    []storage.SeriesKey{{Name: "idle_metric"}},
		results: nil, // no points
	}
	sender := NewSender("http://unused.invalid", reader, nil)
	if err := sender.sendOnce(t.Context(), 30*time.Second); err != nil {
		t.Fatalf("sendOnce with nothing to send should not error: %v", err)
	}
}
