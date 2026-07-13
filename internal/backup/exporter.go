package backup

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

// Exporter ticks on a cron schedule (DESIGN/07 §7.3 suggests robfig/cron
// for calendar schedules — config.yaml's backup.schedule is already
// documented in cron syntax, e.g. "*/15 * * * *"), reading whatever's new
// since the last persisted watermark from source and appending it to
// Iceberg via writer. Used identically for push mode (source is a
// LocalSource) and the central pull-mode agent (source is a remoteSource
// per configured node) — see backup.go's package doc.
type Exporter struct {
	source     DeltaSource
	writer     *IcebergWriter
	watermarks *WatermarkStore
	logger     *slog.Logger
}

func NewExporter(source DeltaSource, writer *IcebergWriter, watermarks *WatermarkStore, logger *slog.Logger) *Exporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Exporter{source: source, writer: writer, watermarks: watermarks, logger: logger}
}

// Run blocks until ctx is done, exporting once immediately and then on
// every firing of the cron schedule.
func (e *Exporter) Run(ctx context.Context, schedule string) error {
	c := cron.New()
	if _, err := c.AddFunc(schedule, func() { e.exportOnce(ctx) }); err != nil {
		return err
	}

	e.exportOnce(ctx)

	c.Start()
	<-ctx.Done()
	<-c.Stop().Done()
	return nil
}

// exportOnce is one CDC cycle: read the persisted watermark, pull
// everything since it from source, append to Iceberg, and only then
// advance the watermark — per DESIGN/07 §7.2, if the commit fails the
// watermark doesn't move, so the next run safely re-reads the same
// window rather than losing it. Errors are logged, not returned/panicked:
// a transient catalog/S3 outage should just skip this run and retry next
// cycle (§7.6), not crash the whole process.
func (e *Exporter) exportOnce(ctx context.Context) {
	start := time.Now()

	since, err := e.watermarks.Load()
	if err != nil {
		observeExport(start, 0, "error")
		e.logger.Error("backup: loading watermark failed", "error", err)
		return
	}

	rows, newWatermark, err := e.source.DeltaRange(ctx, since)
	if err != nil {
		observeExport(start, 0, "error")
		e.logger.Warn("backup: reading delta failed, will retry next cycle", "error", err)
		return
	}
	if len(rows) == 0 {
		observeExport(start, 0, "empty")
		e.logger.Debug("backup: nothing new since last watermark", "since", since)
		return
	}

	if err := e.writer.Append(ctx, rows); err != nil {
		observeExport(start, 0, "error")
		e.logger.Warn("backup: iceberg append failed, watermark not advanced, will retry next cycle", "error", err, "rows", len(rows))
		return
	}
	observeExport(start, len(rows), "ok")

	if err := e.watermarks.Save(newWatermark); err != nil {
		// The Iceberg commit already succeeded — losing the watermark update
		// means the next run re-exports this window (duplicate rows in the
		// lake, not lost data), so this is a warn, not a reason to have
		// skipped the append above (already counted as "ok" - the export did
		// succeed, only the local bookkeeping after it failed).
		e.logger.Warn("backup: exported rows but failed to persist new watermark - next run will re-export this window", "error", err)
		return
	}

	e.logger.Info("backup: exported", "rows", len(rows), "watermark", newWatermark.Format(time.RFC3339))
}
