// Package remotewrite implements both directions of DESIGN/04 §4.4: an
// /api/v1/write receiver (feature-flagged push.receive) and an outbound
// sender (feature-flagged push.send), both speaking the Prometheus
// remote-write wire format — protobuf (prompb.WriteRequest) compressed with
// Snappy's block format — so any existing remote-write-compatible sender or
// receiver interoperates with circa unmodified.
package remotewrite

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"

	"github.com/teochenglim/circa/internal/ingest"
)

// defaultInterval is used for received samples, whose true source-side
// scrape interval isn't known — matches the common remote-write agent
// default (Prometheus itself scrapes at 15s by default).
const defaultInterval = 15 * time.Second

// Handler receives every sample decoded from an incoming write request.
type Handler func(ingest.Sample)

// ReceiveHandler implements POST <push.receive.path>: decode a
// Snappy-compressed prompb.WriteRequest body and hand each sample to
// handler. Any existing remote-write sender (Prometheus, Grafana Alloy,
// Vector, OpenTelemetry Collector) can push here unmodified.
func ReceiveHandler(handler Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		compressed, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
			return
		}

		data, err := snappy.Decode(nil, compressed)
		if err != nil {
			http.Error(w, "snappy decode: "+err.Error(), http.StatusBadRequest)
			return
		}

		var req prompb.WriteRequest
		if err := req.Unmarshal(data); err != nil {
			http.Error(w, "protobuf decode: "+err.Error(), http.StatusBadRequest)
			return
		}

		count := 0
		for _, ts := range req.Timeseries {
			name, labels := splitLabels(ts.Labels)
			if name == "" {
				continue // no __name__ label - nothing to store this sample under
			}
			for _, s := range ts.Samples {
				handler(ingest.Sample{
					Name:     name,
					Labels:   labels,
					Time:     time.UnixMilli(s.Timestamp),
					Value:    s.Value,
					Interval: defaultInterval,
				})
				count++
			}
		}
		logger.Debug("remote-write received", "series", len(req.Timeseries), "samples", count)

		w.WriteHeader(http.StatusNoContent)
	})
}

// splitLabels pulls __name__ out of a prompb label set (Prometheus's own
// convention for carrying the metric name inside the label list) and
// returns the rest as a plain label map.
func splitLabels(pb []prompb.Label) (name string, labels map[string]string) {
	labels = make(map[string]string, len(pb))
	for _, l := range pb {
		if l.Name == "__name__" {
			name = l.Value
			continue
		}
		labels[l.Name] = l.Value
	}
	return name, labels
}
