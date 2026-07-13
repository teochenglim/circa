package backup

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWatermarkStore_LoadMissingFileReturnsZeroTime(t *testing.T) {
	store := NewWatermarkStore(filepath.Join(t.TempDir(), "watermark.json"))
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("got %v, want zero time", got)
	}
}

func TestWatermarkStore_SaveThenLoadRoundTrips(t *testing.T) {
	store := NewWatermarkStore(filepath.Join(t.TempDir(), "nested", "watermark.json"))
	want := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWatermarkStore_SaveOverwritesPreviousValue(t *testing.T) {
	store := NewWatermarkStore(filepath.Join(t.TempDir(), "watermark.json"))
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if err := store.Save(first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := store.Save(second); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Equal(second) {
		t.Errorf("got %v, want %v", got, second)
	}
}
