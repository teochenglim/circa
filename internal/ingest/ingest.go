// Package ingest is the seam every metrics source funnels through and every
// consumer fans out from (see ARCHITECTURE.md "Layering"). v0.1.0 wires up a
// single source (internal/ingest/scrape) and a single consumer
// (internal/storage); later milestones add influx/remotewrite sources and
// alert/anomaly/backup consumers without either side needing to change.
package ingest

import "time"

// Sample is the one shape every ingestion source normalizes into before
// handing data to a Pipeline.
type Sample struct {
	Name     string
	Labels   map[string]string
	Time     time.Time
	Value    float64
	Interval time.Duration // source's collection interval, used to size storage's ring buffer

	// Anomalous is set by the caller (cmd/circa, when features.ml is on)
	// before Ingest is called — internal/anomaly.Detector.Score runs ahead
	// of the pipeline, not as a fanned-out Consumer, so the bit is already
	// known by the time internal/storage embeds it into the stored value
	// and internal/alert can key an "anomaly" condition off it. See
	// DESIGN/06 §6.2 and RELEASE/v0.4.0.md for why.
	Anomalous bool
}

// Consumer receives every sample ingested, regardless of source.
type Consumer interface {
	Consume(Sample) error
}

// Pipeline fans a single ingested sample out to every registered consumer.
type Pipeline struct {
	consumers []Consumer
}

func New(consumers ...Consumer) *Pipeline {
	return &Pipeline{consumers: consumers}
}

// Ingest hands s to every consumer, collecting (not stopping on) errors so
// one failing consumer can't block the others.
func (p *Pipeline) Ingest(s Sample) []error {
	var errs []error
	for _, c := range p.consumers {
		if err := c.Consume(s); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
