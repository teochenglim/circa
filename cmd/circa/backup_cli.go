package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/teochenglim/circa/internal/backup"
	"github.com/teochenglim/circa/internal/config"
)

// runBackupAgent implements `circa backup-agent -config <file>` — the
// pull-mode central poller DESIGN/07 §7.3 describes: for every
// backup.nodes entry, it polls that node's GET /api/v1/backup_range on
// backup.schedule and appends the delta to the same Iceberg table push
// mode writes to. Only this process needs catalog/S3 credentials — the
// polled nodes need only be reachable *inbound* from wherever this runs,
// which is usually the easier direction in a locked-down network.
//
// This deliberately runs as its own subcommand/config, not as part of a
// regular node's `circa -config config.yaml` server process: the agent's
// config.yaml is the one with backup.nodes and real catalog credentials
// populated; a polled node's own config.yaml just needs
// features.backup: true / backup.mode: pull to *serve* the endpoint (see
// cmd/circa/main.go), and never touches Iceberg itself.
func runBackupAgent(args []string) error {
	fs := flag.NewFlagSet("backup-agent", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Backup.Mode != "pull" {
		return fmt.Errorf("backup-agent requires backup.mode: pull (got %q)", cfg.Backup.Mode)
	}
	if cfg.Backup.Catalog.URI == "" || cfg.Backup.Catalog.Warehouse == "" {
		return fmt.Errorf("backup-agent requires backup.catalog.uri and backup.catalog.warehouse to be set")
	}
	if len(cfg.Backup.Nodes) == 0 {
		return fmt.Errorf("backup-agent requires at least one entry in backup.nodes")
	}
	if cfg.Backup.Schedule == "" {
		return fmt.Errorf("backup-agent requires backup.schedule to be set")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	writer, err := backup.NewIcebergWriter(ctx, cfg.Backup.Catalog.URI, cfg.Backup.Catalog.Warehouse,
		cfg.Backup.Catalog.S3Endpoint, cfg.Backup.Catalog.S3Region, os.Getenv("CIRCA_BACKUP_CATALOG_TOKEN"))
	if err != nil {
		return fmt.Errorf("connecting to iceberg catalog: %w", err)
	}

	// One watermark file per polled node — each node's delta stream is
	// independent, unlike push mode's single per-process watermark.
	errCh := make(chan error, len(cfg.Backup.Nodes))
	for _, node := range cfg.Backup.Nodes {
		source := backup.NewRemoteSource(node.URL, node.Username, node.Password)
		watermarkPath := filepath.Join(cfg.Storage.Path, "backup_watermarks", sanitizeNodeURL(node.URL)+".json")
		watermarks := backup.NewWatermarkStore(watermarkPath)
		exporter := backup.NewExporter(source, writer, watermarks, logger.With("node", node.URL))

		go func() {
			errCh <- exporter.Run(ctx, cfg.Backup.Schedule)
		}()
	}

	logger.Info("backup-agent started", "nodes", len(cfg.Backup.Nodes), "schedule", cfg.Backup.Schedule)

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// sanitizeNodeURL turns a node URL into a filesystem-safe file name for
// its per-node watermark state file.
func sanitizeNodeURL(u string) string {
	replacer := strings.NewReplacer("://", "_", "/", "_", ":", "_")
	return replacer.Replace(u)
}
