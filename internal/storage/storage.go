// Package storage is circa's RRD-style store: a fixed number of Gorilla-
// compressed chunks per series, round-robin over time, per DESIGN/03 §3.1
// and §3.2. Store represents one tier (raw, minute, or hour — see tiered.go
// for how the three are wired together with rollups); each tier is a
// directory of one subdirectory per series.
//
// Each series keeps a fixed ring of chunksPerSeries chunk files. A chunk
// holds a run of points, Gorilla delta-of-delta-timestamp + XOR-value
// encoded (gorilla.go) — re-encoded from scratch and rewritten to disk on
// every append, which is simple and correct at scrape-interval write rates,
// though not the most efficient possible I/O pattern; revisit if profiling
// says otherwise. A chunk's on-disk size reflects its actual compressed
// content (no padding to a worst-case size), so total directory size is a
// true measure of compression achieved, not just an allocation.
//
// Disk usage per series is still bounded and constant: chunksPerSeries
// slots, each holding at most maxPointsPerChunk points sized off the tier's
// retention and the series' interval, so a series never grows no matter how
// long circa runs — old chunks are simply overwritten in place.
package storage

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

// chunksPerSeries is the fixed ring size, in chunks, for every series in
// every tier. maxPointsPerChunk is derived per-series from retention/
// interval so the ring always covers the configured retention regardless of
// how coarse or fine that series' interval is.
const chunksPerSeries = 8

// SeriesKey identifies one time series by metric name + label set.
type SeriesKey struct {
	Name   string
	Labels map[string]string
}

// String is a canonical, order-independent identity for the key — used only
// to derive a stable on-disk directory name, never returned to callers.
func (k SeriesKey) String() string {
	names := make([]string, 0, len(k.Labels))
	for n := range k.Labels {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(k.Name)
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", n, k.Labels[n])
	}
	b.WriteByte('}')
	return b.String()
}

// Point is one (timestamp, value) sample read back out of a series.
type Point struct {
	Time  time.Time
	Value float64
}

// SeriesResult is one series' points matching a QueryRange call.
type SeriesResult struct {
	Key    SeriesKey
	Points []Point
}

type series struct {
	mu                sync.Mutex
	key               SeriesKey
	interval          time.Duration
	maxPointsPerChunk int
	chunkSeconds      int64
	dir               string

	openChunkIndex  int
	openChunkPoints []chunkPoint
}

// Store holds every series' chunked ring buffer for one tier under one
// directory.
type Store struct {
	mu        sync.RWMutex
	dir       string
	retention time.Duration
	byKey     map[string]*series
	byName    map[string][]*series
}

type seriesMeta struct {
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels"`
	IntervalNs        int64             `json:"interval_ns"`
	MaxPointsPerChunk int               `json:"max_points_per_chunk"`
}

// Open creates dir if needed and reloads any series already persisted there
// from a previous run. retention sizes new series' chunks; series already on
// disk keep the chunk sizing they were created with.
func Open(dir string, retention time.Duration) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating storage dir %s: %w", dir, err)
	}

	s := &Store{
		dir:       dir,
		retention: retention,
		byKey:     make(map[string]*series),
		byName:    make(map[string][]*series),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading storage dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.loadSeries(e.Name()); err != nil {
			return nil, fmt.Errorf("loading series %s: %w", e.Name(), err)
		}
	}

	return s, nil
}

func (s *Store) loadSeries(hash string) error {
	seriesDir := filepath.Join(s.dir, hash)
	metaBytes, err := os.ReadFile(filepath.Join(seriesDir, "meta.json"))
	if err != nil {
		return err
	}
	var meta seriesMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return err
	}

	sr := &series{
		key:               SeriesKey{Name: meta.Name, Labels: meta.Labels},
		interval:          time.Duration(meta.IntervalNs),
		maxPointsPerChunk: meta.MaxPointsPerChunk,
		chunkSeconds:      chunkSeconds(time.Duration(meta.IntervalNs), meta.MaxPointsPerChunk),
		dir:               seriesDir,
		openChunkIndex:    -1,
	}

	// Resume mid-chunk appending correctly: find whichever persisted chunk
	// holds the most recent point and make it the "open" one, rather than
	// starting a fresh chunk that could collide with (and silently lose) an
	// existing partially-written slot on the very next Append.
	var latestSec int64 = -1
	for idx := 0; idx < chunksPerSeries; idx++ {
		points, err := sr.readChunkFile(idx)
		if err != nil {
			return fmt.Errorf("reading chunk %d: %w", idx, err)
		}
		if len(points) == 0 {
			continue
		}
		last := points[len(points)-1].Sec
		if last > latestSec {
			latestSec = last
			sr.openChunkIndex = idx
			sr.openChunkPoints = points
		}
	}

	s.byKey[sr.key.String()] = sr
	s.byName[sr.key.Name] = append(s.byName[sr.key.Name], sr)
	return nil
}

// Append writes one point into key's ring buffer, creating the series
// (chunk-sized from interval and the store's retention) on first write.
func (s *Store) Append(key SeriesKey, interval time.Duration, t time.Time, value float64) error {
	sr, err := s.seriesFor(key, interval)
	if err != nil {
		return err
	}
	return sr.write(t, value)
}

// Consume implements ingest.Consumer so a Store can be registered directly
// with an ingest.Pipeline.
func (s *Store) Consume(sample ingest.Sample) error {
	interval := sample.Interval
	if interval <= 0 {
		interval = time.Second
	}
	key := SeriesKey{Name: sample.Name, Labels: sample.Labels}
	return s.Append(key, interval, sample.Time, sample.Value)
}

// Series lists every series currently tracked by this store — used by the
// UI to discover what's available to chart, and by federation-adjacent
// tooling later.
func (s *Store) Series() []SeriesKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]SeriesKey, 0, len(s.byKey))
	for _, sr := range s.byKey {
		keys = append(keys, sr.key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}

// chunkSeconds derives how much wall-clock time one chunk covers: enough
// points to hold maxPointsPerChunk samples at interval apart.
func chunkSeconds(interval time.Duration, maxPointsPerChunk int) int64 {
	intervalSeconds := int64(interval / time.Second)
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}
	return intervalSeconds * int64(maxPointsPerChunk)
}

func (s *Store) seriesFor(key SeriesKey, interval time.Duration) (*series, error) {
	canonical := key.String()

	s.mu.RLock()
	sr := s.byKey[canonical]
	s.mu.RUnlock()
	if sr != nil {
		return sr, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if sr := s.byKey[canonical]; sr != nil {
		return sr, nil
	}

	totalPoints := int(s.retention / interval)
	if totalPoints < 1 {
		totalPoints = 1
	}
	maxPointsPerChunk := totalPoints / chunksPerSeries
	if maxPointsPerChunk < 1 {
		maxPointsPerChunk = 1
	}

	hash := sha256.Sum256([]byte(canonical))
	seriesDir := filepath.Join(s.dir, hex.EncodeToString(hash[:]))
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating series dir: %w", err)
	}

	meta := seriesMeta{
		Name:              key.Name,
		Labels:            key.Labels,
		IntervalNs:        interval.Nanoseconds(),
		MaxPointsPerChunk: maxPointsPerChunk,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(seriesDir, "meta.json"), metaBytes, 0o644); err != nil {
		return nil, fmt.Errorf("writing series metadata: %w", err)
	}

	newSeries := &series{
		key:               key,
		interval:          interval,
		maxPointsPerChunk: maxPointsPerChunk,
		chunkSeconds:      chunkSeconds(interval, maxPointsPerChunk),
		dir:               seriesDir,
		openChunkIndex:    -1,
	}
	s.byKey[canonical] = newSeries
	s.byName[key.Name] = append(s.byName[key.Name], newSeries)
	return newSeries, nil
}

func (sr *series) chunkFilePath(idx int) string {
	return filepath.Join(sr.dir, fmt.Sprintf("chunk-%d.bin", idx))
}

// readChunkFile decodes chunk idx, or returns nil if that slot has never
// been written.
func (sr *series) readChunkFile(idx int) ([]chunkPoint, error) {
	data, err := os.ReadFile(sr.chunkFilePath(idx))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) < 2 {
		return nil, fmt.Errorf("chunk file too short (%d bytes)", len(data))
	}
	n := int(binary.BigEndian.Uint16(data[0:2]))
	return decodeChunk(data[2:], n)
}

func (sr *series) writeChunkFile(idx int, points []chunkPoint) error {
	if len(points) > 1<<16-1 {
		return fmt.Errorf("chunk holds %d points, exceeds uint16 header capacity", len(points))
	}
	// Self-heal if the series directory went missing (e.g. an external
	// cleanup while circa was running) rather than failing every append
	// for that series until restart.
	if err := os.MkdirAll(sr.dir, 0o755); err != nil {
		return fmt.Errorf("recreating series dir: %w", err)
	}
	header := make([]byte, 2)
	binary.BigEndian.PutUint16(header, uint16(len(points)))
	body := encodeChunk(points)
	return os.WriteFile(sr.chunkFilePath(idx), append(header, body...), 0o644)
}

// write appends one point to whichever chunk its timestamp falls in,
// rotating to a fresh chunk (silently discarding whatever stale data
// occupied that ring slot from chunksPerSeries cycles ago) when the point
// belongs to a different chunk than the one currently open.
func (sr *series) write(t time.Time, value float64) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sec := t.Unix()
	chunkNum := sec / sr.chunkSeconds
	idx := int(((chunkNum % chunksPerSeries) + chunksPerSeries) % chunksPerSeries)

	if idx != sr.openChunkIndex {
		sr.openChunkIndex = idx
		sr.openChunkPoints = nil
	} else if n := len(sr.openChunkPoints); n > 0 && sec <= sr.openChunkPoints[n-1].Sec {
		// Out-of-order or duplicate-second point: dropping it keeps the
		// chunk's timestamps strictly increasing, which the delta-of-delta
		// encoding in gorilla.go requires.
		return nil
	}

	sr.openChunkPoints = append(sr.openChunkPoints, chunkPoint{Sec: sec, Value: value})
	return sr.writeChunkFile(idx, sr.openChunkPoints)
}

func (sr *series) readAll() ([]Point, error) {
	sr.mu.Lock()
	dir := sr.dir
	sr.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil // no data yet (or the dir vanished externally) - not a query error
	}
	if err != nil {
		return nil, err
	}

	var points []Point
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "chunk-") {
			continue
		}
		idxStr := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "chunk-"), ".bin")
		idx, err := parseChunkIndex(idxStr)
		if err != nil {
			continue
		}
		chunkPoints, err := sr.readChunkFile(idx)
		if err != nil {
			return nil, err
		}
		for _, p := range chunkPoints {
			points = append(points, Point{Time: time.Unix(p.Sec, 0), Value: p.Value})
		}
	}
	return points, nil
}

func parseChunkIndex(s string) (int, error) {
	var idx int
	_, err := fmt.Sscanf(s, "%d", &idx)
	return idx, err
}

// QueryRange returns every series named name (optionally narrowed by an
// exact-match label filter) with points in [start, end], sorted by time.
func (s *Store) QueryRange(name string, match map[string]string, start, end time.Time) ([]SeriesResult, error) {
	s.mu.RLock()
	candidates := append([]*series(nil), s.byName[name]...)
	s.mu.RUnlock()

	var results []SeriesResult
	for _, sr := range candidates {
		if !labelsMatch(sr.key.Labels, match) {
			continue
		}
		points, err := sr.readAll()
		if err != nil {
			return nil, fmt.Errorf("reading series %s: %w", sr.key.String(), err)
		}

		var inRange []Point
		for _, p := range points {
			if !p.Time.Before(start) && !p.Time.After(end) {
				inRange = append(inRange, p)
			}
		}
		sort.Slice(inRange, func(i, j int) bool { return inRange[i].Time.Before(inRange[j].Time) })

		results = append(results, SeriesResult{Key: sr.key, Points: inRange})
	}
	return results, nil
}

func labelsMatch(labels, match map[string]string) bool {
	for k, v := range match {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// Close is a no-op today (chunk files are opened/closed per operation, not
// held open) — kept so callers don't need to change when that changes.
func (s *Store) Close() error {
	return nil
}
