package anomaly

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
)

// seriesReader is the subset of *query.Engine Detector needs — narrowed to
// keep this package's dependency on query minimal and testable, same
// pattern as internal/ingest/remotewrite's seriesReader.
type seriesReader interface {
	Series() []storage.SeriesKey
	QueryRange(name string, match map[string]string, tier storage.Tier, r query.Range) ([]query.Result, []query.AggResult, error)
}

// Config controls the ensemble and feature preprocessing — the Go
// counterpart to config.Anomaly, mirroring Netdata's own ml_config.cc
// defaults (see model.go's doc comment and DESIGN/10_ml_summary.md).
type Config struct {
	ModelCount      int // ensemble size (Netdata default: 18)
	TrainingWindow  time.Duration
	RetrainInterval time.Duration
	DiffN           int     // order of differencing (Netdata: 0 or 1)
	SmoothN         int     // rolling-average window after differencing
	LagN            int     // lagged values per feature vector
	ScoreThreshold  float64 // 0..100 - Score() at/above this counts as anomalous
}

// ensemble is one series' models plus the rolling buffer of raw values used
// to build the vector Score checks against — needs DiffN+effective(SmoothN)+
// LagN raw values before it can produce one.
type ensemble struct {
	mu     sync.Mutex
	models []*Model // oldest first; FIFO, capped at Config.ModelCount (see retrainOne)
	buffer []float64
}

func (e *ensemble) rawBufferLen(cfg Config) int {
	smooth := cfg.SmoothN
	if smooth < 1 {
		smooth = 1
	}
	return cfg.DiffN + smooth + cfg.LagN
}

// Detector scores each ingested sample against its series' ensemble
// in-line (Score), and periodically trains new models in the background
// (Run) — see cmd/circa/main.go for why scoring happens before
// ingest.Pipeline.Ingest rather than as a fanned-out Consumer: the anomaly
// bit needs to be known before internal/storage writes the value that
// carries it.
type Detector struct {
	cfg    Config
	reader seriesReader
	logger *slog.Logger

	mu        sync.Mutex
	ensembles map[string]*ensemble // keyed by storage.SeriesKey.String()
}

func New(cfg Config, reader seriesReader, logger *slog.Logger) *Detector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Detector{
		cfg:       cfg,
		reader:    reader,
		logger:    logger,
		ensembles: make(map[string]*ensemble),
	}
}

func (d *Detector) ensembleFor(key storage.SeriesKey) *ensemble {
	canonical := key.String()
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.ensembles[canonical]
	if e == nil {
		e = &ensemble{}
		d.ensembles[canonical] = e
	}
	return e
}

// Score buffers value into key's raw-value window, builds the same
// diff+smooth+lag feature vector Preprocess would for training, and reports
// whether every currently-trained model in the ensemble agrees the
// resulting vector is anomalous (DESIGN/06 §6.2, ml.cc's ml_dimension_predict:
// a single model scoring below threshold short-circuits to "not anomalous").
// Returns false whenever there isn't enough buffered history yet to build a
// vector, or no model has trained yet — a cold-start series never falsely
// flags anomalies before it has a baseline.
func (d *Detector) Score(key storage.SeriesKey, value float64) bool {
	e := d.ensembleFor(key)
	e.mu.Lock()
	defer e.mu.Unlock()

	need := e.rawBufferLen(d.cfg)
	e.buffer = append(e.buffer, value)
	if len(e.buffer) > need {
		e.buffer = e.buffer[len(e.buffer)-need:]
	}
	if len(e.buffer) < need || len(e.models) == 0 {
		return false
	}

	vectors := Preprocess(e.buffer, d.cfg.DiffN, d.cfg.SmoothN, d.cfg.LagN)
	if len(vectors) == 0 {
		return false
	}
	vector := vectors[len(vectors)-1] // the vector ending at the current sample

	for _, m := range e.models {
		if !m.IsAnomalous(vector, d.cfg.ScoreThreshold) {
			return false // any model scoring below threshold vetoes the anomaly (ml.cc)
		}
	}
	return true
}

// Run starts the retraining loop. Series are retrained on a round-robin
// fan-out across RetrainInterval rather than all at once, spreading CPU cost
// — the same goal as Netdata's own queue-pacing (ml.cc divides its allotted
// time by the pending-model queue size) via a simpler mechanism suited to a
// single-process Go implementation. Each retrain appends a freshly trained
// model to that series' ensemble and evicts the oldest once at capacity
// (ml.cc's km_contexts rotation) — over ModelCount*RetrainInterval, the
// ensemble ends up holding models trained at genuinely different points in
// time, not ModelCount copies of "trained on the same recent window."
func (d *Detector) Run(ctx context.Context) {
	if d.cfg.RetrainInterval <= 0 {
		return
	}
	phase := d.cfg.RetrainInterval / retrainFanout
	if phase <= 0 {
		phase = time.Second
	}
	ticker := time.NewTicker(phase)
	defer ticker.Stop()

	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.retrainDueSeries(tick)
			tick++
		}
	}
}

// retrainFanout is how many sub-ticks RetrainInterval is divided into for
// staggering — every known series gets retrained roughly once per
// RetrainInterval, spread across retrainFanout sub-ticks rather than bursting
// all at once.
const retrainFanout = 12

func (d *Detector) retrainDueSeries(tick int) {
	now := time.Now()
	for i, key := range d.reader.Series() {
		if i%retrainFanout != tick%retrainFanout {
			continue
		}
		d.retrainOne(key, now)
	}
}

func (d *Detector) retrainOne(key storage.SeriesKey, now time.Time) {
	results, _, err := d.reader.QueryRange(key.Name, key.Labels, storage.TierRaw, query.Range{
		Start: now.Add(-d.cfg.TrainingWindow),
		End:   now,
	})
	if err != nil || len(results) == 0 {
		return
	}

	values := make([]float64, len(results[0].Points))
	for i, p := range results[0].Points {
		values[i] = p.Value
	}
	vectors := Preprocess(values, d.cfg.DiffN, d.cfg.SmoothN, d.cfg.LagN)
	if len(vectors) < 2 {
		return
	}

	model, err := Train(vectors)
	if err != nil {
		d.logger.Warn("anomaly model training failed", "series", key.String(), "error", err)
		return
	}

	e := d.ensembleFor(key)
	e.mu.Lock()
	e.models = append(e.models, model)
	if d.cfg.ModelCount > 0 && len(e.models) > d.cfg.ModelCount {
		e.models = e.models[len(e.models)-d.cfg.ModelCount:]
	}
	e.mu.Unlock()
}
