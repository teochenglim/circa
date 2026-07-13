// Command circa is the single-binary metrics aggregator. See DESIGN.md and
// ARCHITECTURE.md for what it does and how it's organized.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/teochenglim/circa/internal/alert"
	"github.com/teochenglim/circa/internal/alert/notify"
	"github.com/teochenglim/circa/internal/anomaly"
	"github.com/teochenglim/circa/internal/config"
	"github.com/teochenglim/circa/internal/httpapi"
	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/ingest/remotewrite"
	"github.com/teochenglim/circa/internal/ingest/scrape"
	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

// circa <config|auth> ... are CLI subcommands (DESIGN/08 §8.1.2); with no
// subcommand, circa runs the server, matching every version before v0.3.0.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "config":
			exitOnError(runConfig(os.Args[2:]))
			return
		case "auth":
			exitOnError(runAuth(os.Args[2:]))
			return
		}
	}

	configPath := flag.String("config", "", "path to config.yaml (falls back to defaults if absent)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(*configPath, logger); err != nil {
		logger.Error("circa exited with error", "error", err)
		os.Exit(1)
	}
}

func exitOnError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "circa: "+err.Error())
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

	engine := query.New(store)

	// Anomaly detection is feature-flagged off by default (DESIGN/06 §6.2) —
	// the single most CPU-hungry optional subsystem. When on, Score runs
	// ahead of ingest.Pipeline.Ingest (not as a fanned-out Consumer) so the
	// anomaly bit is already known by the time internal/storage embeds it
	// into the value it writes — see internal/anomaly's package doc comment.
	var detector *anomaly.Detector
	if cfg.Features.ML {
		detector = anomaly.New(anomaly.Config{
			ModelCount:      cfg.Anomaly.ModelCount,
			TrainingWindow:  time.Duration(cfg.Anomaly.TrainingWindow),
			RetrainInterval: time.Duration(cfg.Anomaly.RetrainInterval),
			DiffN:           cfg.Anomaly.DiffN,
			SmoothN:         cfg.Anomaly.SmoothN,
			LagN:            cfg.Anomaly.LagN,
			ScoreThreshold:  cfg.Anomaly.ScoreThreshold,
		}, engine, logger)
	}

	// Alerting is feature-flagged off by default (DESIGN/06 §6.1). Rule
	// parsing can only fail on a condition.type config.Validate should
	// already have rejected, so an error here means Validate and NewRule
	// have drifted apart — fail loudly rather than silently drop a rule.
	var alertEngine *alert.Engine
	if cfg.Features.Alerts {
		notifiers := make(map[string]alert.Notifier, len(cfg.Alerting.Notifiers))
		for _, n := range cfg.Alerting.Notifiers {
			switch n.Type {
			case "webhook":
				notifiers[n.Name] = notify.NewWebhook(n.URL)
			case "slack":
				notifiers[n.Name] = notify.NewSlack(n.URL)
			}
		}
		rules := make([]alert.Rule, 0, len(cfg.Alerting.Rules))
		for _, rc := range cfg.Alerting.Rules {
			rule, err := alert.NewRule(rc)
			if err != nil {
				return fmt.Errorf("alerting.rules: %w", err)
			}
			rules = append(rules, rule)
		}
		alertEngine = alert.New(rules, notifiers, logger)
	}

	consumers := []ingest.Consumer{store}
	if alertEngine != nil {
		consumers = append(consumers, alertEngine)
	}
	pipeline := ingest.New(consumers...)

	handleSample := func(s ingest.Sample) {
		if detector != nil {
			key := storage.SeriesKey{Name: s.Name, Labels: s.Labels}
			s.Anomalous = detector.Score(key, s.Value)
		}
		if errs := pipeline.Ingest(s); len(errs) > 0 {
			for _, e := range errs {
				logger.Warn("ingest consumer failed", "metric", s.Name, "error", e)
			}
		}
	}

	targets := make([]scrape.Target, 0, len(cfg.Ingest.Scrape.Targets))
	for _, t := range cfg.Ingest.Scrape.Targets {
		targets = append(targets, scrape.Target{
			URL:      t.URL,
			Interval: time.Duration(t.Interval),
			Labels:   t.Labels,
		})
	}
	scraper := scrape.New(targets, handleSample, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scraper.Run(ctx)
	if detector != nil {
		go detector.Run(ctx)
	}

	// Push receive/send are both feature-flagged off by default (DESIGN/04
	// §4.4) — pull scraping remains the zero-config path.
	var writeReceiver http.Handler
	if cfg.Features.PushReceive {
		writeReceiver = remotewrite.ReceiveHandler(handleSample, logger)
	}
	if cfg.Features.PushSend {
		sender := remotewrite.NewSender(cfg.Push.Send.URL, engine, logger)
		go sender.Run(ctx, time.Duration(cfg.Push.Send.Interval))
	}

	server := &http.Server{
		Addr: cfg.Server.ListenAddress,
		Handler: httpapi.NewRouter(engine, httpapi.Options{
			Config:        cfg,
			WriteReceiver: writeReceiver,
			AlertEngine:   alertEngine,
		}),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("circa listening", "address", cfg.Server.ListenAddress, "targets", len(targets),
			"auth", len(cfg.Auth.Users) > 0, "push_receive", cfg.Features.PushReceive, "push_send", cfg.Features.PushSend,
			"alerts", cfg.Features.Alerts, "ml", cfg.Features.ML)
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
