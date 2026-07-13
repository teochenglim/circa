// Package scrape pulls metrics from Prometheus-exposition-format endpoints
// on a per-target ticker, per DESIGN/04 §4.2. It never needs to know what's
// on the other end — node_exporter, postgres_exporter, a hand-rolled app's
// /metrics, or an existing Prometheus server's /federate all parse the same
// way via prometheus/common/expfmt.
package scrape

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/teochenglim/circa/internal/ingest"
)

// Target is one entry from ingest.scrape.targets in config.yaml.
type Target struct {
	URL      string
	Interval time.Duration
	Labels   map[string]string
}

// Handler receives every sample decoded from a single scrape.
type Handler func(ingest.Sample)

// Scraper runs one ticker per target, GETs its URL, and hands decoded
// samples to Handler. The zero target list means Run does nothing —
// matching Prometheus's own "empty scrape_configs does nothing" behavior.
type Scraper struct {
	targets []Target
	handler Handler
	client  *http.Client
	logger  *slog.Logger
}

func New(targets []Target, handler Handler, logger *slog.Logger) *Scraper {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scraper{
		targets: targets,
		handler: handler,
		client:  &http.Client{Timeout: 10 * time.Second},
		logger:  logger,
	}
}

// Run blocks until ctx is canceled, scraping every target on its own
// interval. Each target gets an immediate first scrape, then ticks.
func (s *Scraper) Run(ctx context.Context) {
	if len(s.targets) == 0 {
		<-ctx.Done()
		return
	}

	done := make(chan struct{}, len(s.targets))
	for _, t := range s.targets {
		go func(t Target) {
			s.runTarget(ctx, t)
			done <- struct{}{}
		}(t)
	}
	for range s.targets {
		<-done
	}
}

func (s *Scraper) runTarget(ctx context.Context, t Target) {
	s.scrapeOnce(ctx, t)

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scrapeOnce(ctx, t)
		}
	}
}

func (s *Scraper) scrapeOnce(ctx context.Context, t Target) {
	samples, err := s.fetch(ctx, t)
	if err != nil {
		s.logger.Warn("scrape failed", "target", t.URL, "error", err)
		return
	}
	for _, sample := range samples {
		s.handler(sample)
	}
}

func (s *Scraper) fetch(ctx context.Context, t Target) ([]ingest.Sample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", t.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %s", t.URL, resp.Status)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", t.URL, err)
	}

	now := time.Now()
	var samples []ingest.Sample
	for name, family := range families {
		samples = append(samples, familySamples(name, family, t, now)...)
	}
	return samples, nil
}

// familySamples flattens one MetricFamily into Circa's flat Sample shape.
// Histograms/summaries have no single scalar value, so they're expanded
// into their component series (_bucket/_sum/_count, quantile/_sum/_count)
// the same way Prometheus itself represents them internally.
func familySamples(name string, family *dto.MetricFamily, t Target, scrapeTime time.Time) []ingest.Sample {
	var out []ingest.Sample
	for _, m := range family.GetMetric() {
		ts := scrapeTime
		if m.TimestampMs != nil {
			ts = time.UnixMilli(m.GetTimestampMs())
		}
		labels := mergeLabels(t.Labels, m.GetLabel())

		switch {
		case m.Counter != nil:
			out = append(out, sample(name, labels, m.Counter.GetValue(), ts, t.Interval))
		case m.Gauge != nil:
			out = append(out, sample(name, labels, m.Gauge.GetValue(), ts, t.Interval))
		case m.Untyped != nil:
			out = append(out, sample(name, labels, m.Untyped.GetValue(), ts, t.Interval))
		case m.Histogram != nil:
			h := m.Histogram
			out = append(out, sample(name+"_sum", labels, h.GetSampleSum(), ts, t.Interval))
			out = append(out, sample(name+"_count", labels, float64(h.GetSampleCount()), ts, t.Interval))
			for _, b := range h.GetBucket() {
				bucketLabels := withLabel(labels, "le", formatBound(b.GetUpperBound()))
				out = append(out, sample(name+"_bucket", bucketLabels, float64(b.GetCumulativeCount()), ts, t.Interval))
			}
		case m.Summary != nil:
			sm := m.Summary
			out = append(out, sample(name+"_sum", labels, sm.GetSampleSum(), ts, t.Interval))
			out = append(out, sample(name+"_count", labels, float64(sm.GetSampleCount()), ts, t.Interval))
			for _, q := range sm.GetQuantile() {
				qLabels := withLabel(labels, "quantile", formatBound(q.GetQuantile()))
				out = append(out, sample(name, qLabels, q.GetValue(), ts, t.Interval))
			}
		}
	}
	return out
}

func sample(name string, labels map[string]string, value float64, ts time.Time, interval time.Duration) ingest.Sample {
	return ingest.Sample{
		Name:     name,
		Labels:   labels,
		Value:    value,
		Time:     ts,
		Interval: interval,
	}
}

// mergeLabels applies the target's configured labels first, then lets the
// exposition format's own labels win on collision — the metric's own
// identity takes precedence over target-level enrichment.
func mergeLabels(targetLabels map[string]string, pairs []*dto.LabelPair) map[string]string {
	labels := make(map[string]string, len(targetLabels)+len(pairs))
	for k, v := range targetLabels {
		labels[k] = v
	}
	for _, p := range pairs {
		labels[p.GetName()] = p.GetValue()
	}
	return labels
}

func withLabel(labels map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out[key] = value
	return out
}

func formatBound(f float64) string {
	return fmt.Sprintf("%g", f)
}
