// Command circa is the single-binary metrics aggregator. See DESIGN.md and
// ARCHITECTURE.md for what it does and how it's organized.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/teochenglim/circa/internal/config"
	"github.com/teochenglim/circa/internal/httpapi"
	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/ingest/scrape"
	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml (falls back to defaults if absent)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(*configPath, logger); err != nil {
		logger.Error("circa exited with error", "error", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	store, err := storage.OpenTiered(cfg.Storage.Path,
		time.Duration(cfg.Storage.Retention.Raw),
		time.Duration(cfg.Storage.Retention.Minute),
		time.Duration(cfg.Storage.Retention.Hour),
	)
	if err != nil {
		return err
	}
	defer store.Close()

	pipeline := ingest.New(store)

	targets := make([]scrape.Target, 0, len(cfg.Ingest.Scrape.Targets))
	for _, t := range cfg.Ingest.Scrape.Targets {
		targets = append(targets, scrape.Target{
			URL:      t.URL,
			Interval: time.Duration(t.Interval),
			Labels:   t.Labels,
		})
	}
	scraper := scrape.New(targets, func(s ingest.Sample) {
		if errs := pipeline.Ingest(s); len(errs) > 0 {
			for _, e := range errs {
				logger.Warn("ingest consumer failed", "metric", s.Name, "error", e)
			}
		}
	}, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scraper.Run(ctx)

	engine := query.New(store)
	server := &http.Server{
		Addr:    cfg.Server.ListenAddress,
		Handler: httpapi.NewRouter(engine),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("circa listening", "address", cfg.Server.ListenAddress, "targets", len(targets))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		return err
	}
}
