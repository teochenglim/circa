package remotewrite

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

// seriesReader is the subset of *query.Engine the Sender needs — narrowed
// to keep this package's dependency on query minimal and testable.
type seriesReader interface {
	Series() []storage.SeriesKey
	QueryRange(name string, match map[string]string, tier storage.Tier, r query.Range) ([]query.Result, []query.AggResult, error)
}

// Sender periodically batches every raw sample ingested since its last run
// (across every known series) and pushes it, in remote-write wire format, to
// an external endpoint — the outbound half of DESIGN/04 §4.4.2. Watermarks
// are kept in memory only: a restart re-sends up to one interval's worth of
// overlap, which the receiving end's storage will simply overwrite —
// acceptable for v0.3.0, revisit if exactly-once delivery ever matters here.
type Sender struct {
	url        string
	client     *http.Client
	engine     seriesReader
	logger     *slog.Logger
	watermarks map[string]time.Time
}

func NewSender(url string, engine seriesReader, logger *slog.Logger) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{
		url:        url,
		client:     &http.Client{Timeout: 10 * time.Second},
		engine:     engine,
		logger:     logger,
		watermarks: make(map[string]time.Time),
	}
}

// Run pushes on every tick of interval until ctx is done.
func (s *Sender) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sendOnce(ctx, interval); err != nil {
				s.logger.Warn("remote-write send failed", "url", s.url, "error", err)
			}
		}
	}
}

func (s *Sender) sendOnce(ctx context.Context, interval time.Duration) (err error) {
	start := time.Now()
	defer func() { observeSend(start, err) }()

	now := time.Now()
	keys := s.engine.Series()

	timeseries := make([]prompb.TimeSeries, 0, len(keys))
	for _, key := range keys {
		wmKey := key.String()
		since, seen := s.watermarks[wmKey]
		if !seen {
			since = now.Add(-interval)
		}

		results, _, err := s.engine.QueryRange(key.Name, key.Labels, storage.TierRaw, query.Range{Start: since, End: now})
		if err != nil {
			return fmt.Errorf("querying %s: %w", wmKey, err)
		}
		for _, res := range results {
			if len(res.Points) == 0 {
				continue
			}
			timeseries = append(timeseries, toTimeSeries(res))
		}
		s.watermarks[wmKey] = now
	}

	if len(timeseries) == 0 {
		return nil
	}
	return s.push(ctx, timeseries)
}

func toTimeSeries(res query.Result) prompb.TimeSeries {
	labels := make([]prompb.Label, 0, len(res.Metric))
	for k, v := range res.Metric {
		labels = append(labels, prompb.Label{Name: k, Value: v})
	}
	samples := make([]prompb.Sample, 0, len(res.Points))
	for _, p := range res.Points {
		samples = append(samples, prompb.Sample{Value: p.Value, Timestamp: p.Time.UnixMilli()})
	}
	return prompb.TimeSeries{Labels: labels, Samples: samples}
}

func (s *Sender) push(ctx context.Context, timeseries []prompb.TimeSeries) error {
	req := &prompb.WriteRequest{Timeseries: timeseries}
	data, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("marshaling write request: %w", err)
	}
	compressed := snappy.Encode(nil, data)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("remote endpoint returned %s", resp.Status)
	}
	s.logger.Debug("remote-write sent", "url", s.url, "series", len(timeseries))
	return nil
}
