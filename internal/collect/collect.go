// Package collect is circa's built-in, in-binary system metrics source —
// /proc+/sys on Linux, sysctl/vm_stat/netstat/top on macOS — added in
// v0.5.0 (see RELEASE/v0.5.0.md) so a fresh install has real host metrics
// with zero external exporter required.
//
// This is a deliberate second reversal of DESIGN/04 §4.1's "wrapper, not a
// fork" decision; see RELEASE/v0.5.0.md for why. Every parser in this
// package is a from-scratch reimplementation: node_exporter (Apache-2.0)
// and netdata (GPL-3.0) are read as reference material for field layouts
// and edge cases, never imported or vendored — see this package's
// per-platform files and RELEASE/v0.5.0.md's "Reuse policy" section.
//
// Platform scope for v0.5.0 is Linux and macOS only; Windows and every
// other target is deferred to v1.1.0 (RELEASE/v1.1.0.md) — Supported
// reports false there rather than silently collecting nothing.
package collect

import (
	"context"
	"log/slog"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

// Handler receives every sample produced by one collection tick.
type Handler func(ingest.Sample)

// Collector runs collectAll (the active build's platform implementation) on
// its own ticker, mirroring internal/ingest/scrape's per-target ticker
// shape even though there's only one "target" here: the local host.
type Collector struct {
	handler Handler
	logger  *slog.Logger

	// labels are merged onto every sample this Collector produces —
	// notably pod/namespace/node in a Kubernetes DaemonSet, where every
	// pod's self-collected metrics would otherwise be indistinguishable
	// from every other node's (see k8s/20-daemonset.yaml, helm/circa's
	// daemonset.yaml, and cmd/circa/main.go, which populate this from the
	// Downward API's POD_NAME/POD_NAMESPACE/NODE_NAME env vars). A
	// sample's own labels (device, cpu, mode, ...) always win on
	// collision — see mergeStaticLabels.
	labels map[string]string
}

func New(handler Handler, logger *slog.Logger, labels map[string]string) *Collector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{handler: handler, logger: logger, labels: labels}
}

// Run blocks until ctx is canceled, collecting immediately and then on
// every tick of interval.
func (c *Collector) Run(ctx context.Context, interval time.Duration) {
	c.collectOnce(interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collectOnce(interval)
		}
	}
}

func (c *Collector) collectOnce(interval time.Duration) {
	samples, err := collectAll(time.Now(), interval)
	if err != nil {
		c.logger.Warn("local system collection failed", "error", err)
		return
	}
	for _, s := range samples {
		if len(c.labels) > 0 {
			s.Labels = mergeStaticLabels(s.Labels, c.labels)
		}
		c.handler(s)
	}
}

// mergeStaticLabels overlays extra onto labels without mutating either
// input, keeping labels' own values on any key collision — a sample's
// identity (device="sda", mode="user", ...) must never be shadowed by a
// deployment-level label sharing the same key.
func mergeStaticLabels(labels, extra map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+len(extra))
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// sample builds one ingest.Sample with the current time and given interval
// — every platform collector's samples are stamped at collection time, not
// at some notion of "when the kernel counter last changed" (which none of
// /proc, sysctl, or the shelled-out tools expose).
func sample(name string, labels map[string]string, value float64, t time.Time, interval time.Duration) ingest.Sample {
	return ingest.Sample{
		Name:     name,
		Labels:   labels,
		Value:    value,
		Time:     t,
		Interval: interval,
	}
}

// charsToString trims a NUL-terminated fixed-size byte array (the shape
// golang.org/x/sys/unix.Utsname's fields come in on both Linux and darwin)
// down to a Go string.
func charsToString(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}
