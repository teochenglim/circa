package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WatermarkStore persists the single global "last exported timestamp" to a
// small local JSON file, per DESIGN/07 §7.2 ("track a watermark... in a
// local state file") — unlike internal/ingest/remotewrite's Sender, whose
// in-memory-only watermark is an accepted v0.3.0 simplification because a
// resend on restart is harmless there. Backup's watermark must survive a
// restart: without it, a restart would either re-export everything since
// the beginning of retention (duplicate rows the next query would have to
// dedupe) or silently skip whatever was collected while the process was
// down.
//
// One watermark shared across every series, not per-series — DESIGN/07 §7.2
// says "per metric or per shard" is an option, but a single node's tier-1
// data is small enough that one sweep across every series per run (see
// CollectDelta) is simpler and sufficient; per-series state file sprawl
// isn't worth it at this scale.
type WatermarkStore struct {
	path string
	mu   sync.Mutex
}

func NewWatermarkStore(path string) *WatermarkStore {
	return &WatermarkStore{path: path}
}

type watermarkFile struct {
	Watermark time.Time `json:"watermark"`
}

// Load returns the persisted watermark, or the zero time if the file
// doesn't exist yet (first run ever — CollectDelta's since=zero-time reads
// everything currently in tier-1 storage).
func (s *WatermarkStore) Load() (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var f watermarkFile
	if err := json.Unmarshal(data, &f); err != nil {
		return time.Time{}, err
	}
	return f.Watermark, nil
}

// Save persists the watermark, creating the parent directory if needed.
// Callers must only call this after the corresponding Iceberg commit
// succeeds (DESIGN/07 §7.2: "advance the watermark only after the Iceberg
// commit succeeds") — Save itself doesn't enforce that ordering, it just
// writes whatever it's given.
func (s *WatermarkStore) Save(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(watermarkFile{Watermark: t})
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
