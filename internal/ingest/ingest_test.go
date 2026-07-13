package ingest

import (
	"errors"
	"testing"
)

type recordingConsumer struct {
	received []Sample
	err      error
}

func (c *recordingConsumer) Consume(s Sample) error {
	c.received = append(c.received, s)
	return c.err
}

func TestPipelineFansOutToEveryConsumer(t *testing.T) {
	a := &recordingConsumer{}
	b := &recordingConsumer{}
	p := New(a, b)

	sample := Sample{Name: "up", Value: 1}
	if errs := p.Ingest(sample); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(a.received) != 1 || a.received[0].Name != "up" {
		t.Errorf("consumer a did not receive sample: %+v", a.received)
	}
	if len(b.received) != 1 || b.received[0].Name != "up" {
		t.Errorf("consumer b did not receive sample: %+v", b.received)
	}
}

func TestPipelineCollectsErrorsWithoutStoppingOtherConsumers(t *testing.T) {
	failing := &recordingConsumer{err: errors.New("boom")}
	ok := &recordingConsumer{}
	p := New(failing, ok)

	errs := p.Ingest(Sample{Name: "up"})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if len(ok.received) != 1 {
		t.Errorf("second consumer should still receive the sample despite the first failing")
	}
}

func TestPipelineWithNoConsumers(t *testing.T) {
	p := New()
	if errs := p.Ingest(Sample{Name: "up"}); len(errs) != 0 {
		t.Errorf("expected no errors with zero consumers, got %v", errs)
	}
}
