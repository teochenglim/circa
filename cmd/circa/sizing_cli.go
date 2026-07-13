package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/teochenglim/circa/internal/config"
)

// rawPointBytes is the uncompressed size of one (timestamp, value) point —
// the same 8+8 byte pair internal/storage's Gorilla encoder compresses down
// from, and the baseline v0.2.0's measured compression ratio is relative to
// (see RELEASE/v0.2.0.md).
const rawPointBytes = 16

// defaultCompressionRatio is v0.2.0's actually-measured ratio for a
// realistic mix of constant/counter/random-walk metrics (RELEASE/v0.2.0.md)
// — not DESIGN/03 §3.3's ~10x aspiration, which that measurement fell short
// of at this chunk size. A synthetic constant-value series in isolation hits
// 55.2x; -ratio lets a caller override with whatever a real trial measured
// for their own metric mix instead of trusting either extreme.
const defaultCompressionRatio = 3.14

// rollupSubseries is the min/avg/max triplet internal/storage/tiered.go
// stores per metric for the minute/hour tiers (three ordinary series
// reusing the tier-0 engine, DESIGN/03 §3.4) — tripling their point count
// relative to a single raw-tier series.
const rollupSubseries = 3

// runSizing implements `circa sizing` (DESIGN/03 §3.3): estimate on-disk
// footprint from metric count, resolution, and per-tier retention, so a
// deployment can reason about disk budget before turning on a scrape target
// fleet — the same role Netdata's own dbengine calculator plays.
func runSizing(args []string) error {
	fs := flag.NewFlagSet("sizing", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.yaml to read retention/interval defaults from (falls back to circa's own defaults if absent)")
	metrics := fs.Int("metrics", 100, "distinct metric count (series) to size for — include every scrape target's exposed metrics plus circa's own self-collected set")
	interval := fs.Duration("interval", 0, "raw collection/scrape interval override (default: config's ingest.collect.interval, or 15s)")
	rawRetention := fs.Duration("retention.raw", 0, "override storage.retention.raw")
	minuteRetention := fs.Duration("retention.minute", 0, "override storage.retention.minute")
	hourRetention := fs.Duration("retention.hour", 0, "override storage.retention.hour")
	ratio := fs.Float64("ratio", defaultCompressionRatio, "assumed compression ratio (default: v0.2.0's measured realistic-mix ratio; use 55.2 for a best-case constant-value estimate, 1 for uncompressed)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *metrics <= 0 {
		return fmt.Errorf("-metrics must be positive")
	}
	if *ratio <= 0 {
		return fmt.Errorf("-ratio must be positive")
	}

	cfg := config.Default()
	if *configPath != "" {
		loaded, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		cfg = loaded
	}

	rawInterval := time.Duration(cfg.Ingest.Collect.Interval)
	if rawInterval <= 0 {
		rawInterval = config.DefaultCollectInterval
	}
	if *interval > 0 {
		rawInterval = *interval
	}

	raw := time.Duration(cfg.Storage.Retention.Raw)
	if *rawRetention > 0 {
		raw = *rawRetention
	}
	minute := time.Duration(cfg.Storage.Retention.Minute)
	if *minuteRetention > 0 {
		minute = *minuteRetention
	}
	hour := time.Duration(cfg.Storage.Retention.Hour)
	if *hourRetention > 0 {
		hour = *hourRetention
	}

	tiers := []struct {
		name      string
		points    float64
		subseries int
	}{
		{"raw", tierPoints(raw, rawInterval), 1},
		{"minute", tierPoints(minute, time.Minute), rollupSubseries},
		{"hour", tierPoints(hour, time.Hour), rollupSubseries},
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "circa sizing estimate — %d metrics, %s raw interval, %.2fx compression ratio\n\n", *metrics, rawInterval, *ratio)
	fmt.Fprintln(w, "TIER\tRETENTION\tPOINTS/SERIES\tUNCOMPRESSED\tESTIMATED ON-DISK")

	var totalUncompressed, totalCompressed float64
	retentions := map[string]time.Duration{"raw": raw, "minute": minute, "hour": hour}
	for _, t := range tiers {
		seriesCount := float64(*metrics) * float64(t.subseries)
		uncompressed := seriesCount * t.points * rawPointBytes
		compressed := uncompressed / *ratio
		totalUncompressed += uncompressed
		totalCompressed += compressed
		fmt.Fprintf(w, "%s\t%s\t%.0f\t%s\t%s\n", t.name, retentions[t.name], t.points, humanBytes(uncompressed), humanBytes(compressed))
	}
	fmt.Fprintf(w, "TOTAL\t\t\t%s\t%s\n", humanBytes(totalUncompressed), humanBytes(totalCompressed))
	return w.Flush()
}

// tierPoints is how many raw points one series accumulates over retention at
// the given per-point interval — 0 if the tier is disabled (retention <= 0).
func tierPoints(retention, interval time.Duration) float64 {
	if retention <= 0 || interval <= 0 {
		return 0
	}
	return float64(retention) / float64(interval)
}

func humanBytes(b float64) string {
	const unit = 1024.0
	if b < unit {
		return fmt.Sprintf("%.0fB", b)
	}
	div, exp := unit, 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%ciB", b/div, "KMGTPE"[exp])
}
