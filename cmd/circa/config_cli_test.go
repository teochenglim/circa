package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/teochenglim/circa/internal/config"
)

func TestRunConfigInitWritesLoadableConfig(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "config.yaml")

	if err := runConfigInit([]string{"-output", out, "-profile", "minimal"}); err != nil {
		t.Fatalf("runConfigInit: %v", err)
	}
	if err := runConfigCheck([]string{out}); err != nil {
		t.Fatalf("runConfigCheck: %v", err)
	}

	if err := runConfigInit([]string{"-output", out}); err == nil {
		t.Error("expected error when -output already exists")
	}
}

func TestRunConfigInitFullProfileEnablesFeatures(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "config.yaml")

	if err := runConfigInit([]string{"-output", out, "-profile", "full"}); err != nil {
		t.Fatalf("runConfigInit: %v", err)
	}

	cfg, err := config.Load(out)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Features.PushSend || !cfg.Features.PushReceive || !cfg.Features.Backup {
		t.Errorf("full profile should enable push/backup features, got %+v", cfg.Features)
	}
}

func TestRunConfigCheckMissingFile(t *testing.T) {
	if err := runConfigCheck([]string{filepath.Join(t.TempDir(), "nope.yaml")}); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRunConfigInitRejectsBadProfile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "config.yaml")
	if err := runConfigInit([]string{"-output", out, "-profile", "bogus"}); err == nil {
		t.Error("expected error for unknown profile")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("no file should be written when template generation fails")
	}
}
