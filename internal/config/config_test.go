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

func TestDurationParsesDayAndYearUnits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
storage:
  retention:
    raw: 1h
    minute: 7d
    hour: 1y
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := time.Duration(cfg.Storage.Retention.Minute); got != 7*24*time.Hour {
		t.Errorf("Retention.Minute = %v, want 168h (7d)", got)
	}
	if got := time.Duration(cfg.Storage.Retention.Hour); got != 365*24*time.Hour {
		t.Errorf("Retention.Hour = %v, want 8760h (1y)", got)
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

func TestLoadDefaultsAnomalyToNetdataDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Anomaly.ModelCount != 18 {
		t.Errorf("ModelCount = %d, want 18", cfg.Anomaly.ModelCount)
	}
	if cfg.Anomaly.DiffN != 1 || cfg.Anomaly.SmoothN != 3 || cfg.Anomaly.LagN != 5 {
		t.Errorf("DiffN/SmoothN/LagN = %d/%d/%d, want 1/3/5", cfg.Anomaly.DiffN, cfg.Anomaly.SmoothN, cfg.Anomaly.LagN)
	}
	if cfg.Anomaly.ScoreThreshold != 99.0 {
		t.Errorf("ScoreThreshold = %v, want 99.0", cfg.Anomaly.ScoreThreshold)
	}
}

func TestLoadRejectsAnomalyMisconfigWhenMLEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
features:
  ml: true
anomaly:
  model_count: 0
  diff_n: 5
  lag_n: 0
  score_threshold: 500
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid anomaly config, got nil")
	}
}

func TestLoadAllowsExplicitDiffZero(t *testing.T) {
	// diff_n: 0 is a valid, meaningful value (disables differencing) and
	// must not be silently overridden back to the default of 1.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
features:
  ml: true
anomaly:
  diff_n: 0
  lag_n: 3
  model_count: 2
  training_window: 1h
  retrain_interval: 1h
  score_threshold: 99
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Anomaly.DiffN != 0 {
		t.Errorf("DiffN = %d, want 0 (explicit value must survive)", cfg.Anomaly.DiffN)
	}
}

func TestLoadRejectsAlertRuleUnknownNotifier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
features:
  alerts: true
alerting:
  rules:
    - name: high_cpu
      metric: cpu
      condition: { type: threshold, operator: ">", value: 90 }
      severity: warning
      notify: [nonexistent]
  notifiers: []
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for rule referencing an unknown notifier, got nil")
	}
}

func TestLoadRejectsAlertRuleBadConditionType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
features:
  alerts: true
alerting:
  rules:
    - name: r
      metric: m
      condition: { type: bogus }
  notifiers: []
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown condition type, got nil")
	}
}

func TestLoadRejectsAnomalyConditionWhenMLDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
features:
  alerts: true
alerting:
  rules:
    - name: r
      metric: m
      condition: { type: anomaly }
  notifiers: []
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for anomaly condition with features.ml false, got nil")
	}
}

func TestLoadAcceptsValidAlertingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
features:
  alerts: true
alerting:
  rules:
    - name: high_cpu
      metric: cpu
      labels: { host: a }
      condition: { type: threshold, operator: ">", value: 90 }
      for: 5m
      severity: critical
      notify: [ops]
  notifiers:
    - name: ops
      type: webhook
      url: https://example.internal/hook
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Alerting.Rules) != 1 || cfg.Alerting.Rules[0].Name != "high_cpu" {
		t.Errorf("Rules = %+v", cfg.Alerting.Rules)
	}
}

func TestRedactedMasksAuthAndNotifierSecrets(t *testing.T) {
	cfg := Config{
		Auth: Auth{Users: map[string]string{"admin": "$2a$10$realhashvalue"}},
		Alerting: Alerting{
			Notifiers: []NotifierConfig{{Name: "slack", Type: "slack", URL: "https://hooks.slack.com/services/T000/B000/xxxxTOKEN"}},
		},
	}
	redacted := cfg.Redacted()
	if redacted.Auth.Users["admin"] != "[redacted]" {
		t.Errorf("Auth.Users[admin] = %q, want [redacted]", redacted.Auth.Users["admin"])
	}
	if redacted.Alerting.Notifiers[0].URL != "[redacted]" {
		t.Errorf("Notifiers[0].URL = %q, want [redacted]", redacted.Alerting.Notifiers[0].URL)
	}
	// Original must be untouched.
	if cfg.Alerting.Notifiers[0].URL == "[redacted]" {
		t.Error("Redacted mutated the original Config")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
