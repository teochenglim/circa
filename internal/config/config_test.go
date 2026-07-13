package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.ListenAddress != ":9100" {
		t.Errorf("ListenAddress = %q, want :9100", cfg.Server.ListenAddress)
	}
	if len(cfg.Ingest.Scrape.Targets) != 0 {
		t.Errorf("expected no scrape targets by default, got %v", cfg.Ingest.Scrape.Targets)
	}
	if time.Duration(cfg.Storage.Retention.Raw) != 2*time.Hour {
		t.Errorf("Retention.Raw = %v, want 2h", time.Duration(cfg.Storage.Retention.Raw))
	}
}

func TestLoadEmptyPathReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.Path != "./data" {
		t.Errorf("Storage.Path = %q, want ./data", cfg.Storage.Path)
	}
}

func TestLoadParsesScrapeTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
server:
  listen_address: ":9090"

ingest:
  scrape:
    targets:
      - url: http://localhost:9100/metrics
        interval: 15s
        labels: { job: node }

storage:
  path: /tmp/circa-test
  retention:
    raw: 1h
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.ListenAddress != ":9090" {
		t.Errorf("ListenAddress = %q, want :9090", cfg.Server.ListenAddress)
	}
	if len(cfg.Ingest.Scrape.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(cfg.Ingest.Scrape.Targets))
	}
	target := cfg.Ingest.Scrape.Targets[0]
	if target.URL != "http://localhost:9100/metrics" {
		t.Errorf("URL = %q", target.URL)
	}
	if time.Duration(target.Interval) != 15*time.Second {
		t.Errorf("Interval = %v, want 15s", time.Duration(target.Interval))
	}
	if target.Labels["job"] != "node" {
		t.Errorf("Labels[job] = %q, want node", target.Labels["job"])
	}
	if time.Duration(cfg.Storage.Retention.Raw) != time.Hour {
		t.Errorf("Retention.Raw = %v, want 1h", time.Duration(cfg.Storage.Retention.Raw))
	}
}

func TestLoadRejectsTargetMissingURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
ingest:
  scrape:
    targets:
      - interval: 15s
`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for target missing url, got nil")
	}
}

func TestLoadRejectsTargetMissingInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
ingest:
  scrape:
    targets:
      - url: http://localhost:9100/metrics
`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for target missing interval, got nil")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
