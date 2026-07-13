// Package storage is circa's tier-0 store: one fixed-size, on-disk
// round-robin buffer per series, per DESIGN/03 §3.1. No compression yet
// (that's v0.2.0's Gorilla delta+XOR pass) and no mmap — a plain os.File
// read/write per point is simple, correct, and fast enough at scrape-interval
// write rates; revisit if profiling says otherwise once compression lands.
//
// Disk usage per series is constant: capacity = retention / interval slots,
// each slot a fixed 16-byte (timestamp, value) record, so a series never
// grows no matter how long circa runs — the write position just wraps.
package storage

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

const (
	recordSize = 16 // 8 bytes unix-nano timestamp + 8 bytes float64 value bits
	headerSize = 12 // 4 bytes capacity (uint32) + 8 bytes interval-nanos (int64)
)

// SeriesKey identifies one time series by metric name + label set.
type SeriesKey struct {
	Name   string
	Labels map[string]string
}

// String is a canonical, order-independent identity for the key — used only
// to derive a stable on-disk filename, never returned to callers.
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
	mu       sync.Mutex
	key      SeriesKey
	interval time.Duration
	capacity int
	file     *os.File
}

// Store holds every series' tier-0 ring buffer under one directory.
type Store struct {
	mu        sync.RWMutex
	dir       string
	retention time.Duration
	byKey     map[string]*series
	byName    map[string][]*series
}

type seriesMeta struct {
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	IntervalNs int64             `json:"interval_ns"`
	Capacity   int               `json:"capacity"`
}

// Open creates dir if needed and reloads any series already persisted there
// from a previous run. retention sizes the ring buffer for series created
// from now on; series already on disk keep the capacity they were created
// with.
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		hash := strings.TrimSuffix(e.Name(), ".meta.json")
		if err := s.loadSeries(hash); err != nil {
			return nil, fmt.Errorf("loading series %s: %w", hash, err)
		}
	}

	return s, nil
}

func (s *Store) loadSeries(hash string) error {
	metaBytes, err := os.ReadFile(filepath.Join(s.dir, hash+".meta.json"))
	if err != nil {
		return err
	}
	var meta seriesMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Join(s.dir, hash+".tier0"), os.O_RDWR, 0o644)
	if err != nil {
		return err
	}

	key := SeriesKey{Name: meta.Name, Labels: meta.Labels}
	sr := &series{
		key:      key,
		interval: time.Duration(meta.IntervalNs),
		capacity: meta.Capacity,
		file:     f,
	}
	s.byKey[key.String()] = sr
	s.byName[key.Name] = append(s.byName[key.Name], sr)
	return nil
}

// Append writes one point into key's tier-0 ring buffer, creating the
// series (sized from interval and the store's retention) on first write.
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

	capacity := int(s.retention / interval)
	if capacity < 1 {
		capacity = 1
	}

	hash := sha256.Sum256([]byte(canonical))
	hexHash := hex.EncodeToString(hash[:])

	f, err := os.Create(filepath.Join(s.dir, hexHash+".tier0"))
	if err != nil {
		return nil, fmt.Errorf("creating series file: %w", err)
	}
	if err := f.Truncate(int64(headerSize + capacity*recordSize)); err != nil {
		f.Close()
		return nil, fmt.Errorf("sizing series file: %w", err)
	}

	header := make([]byte, headerSize)
	binary.BigEndian.PutUint32(header[0:4], uint32(capacity))
	binary.BigEndian.PutUint64(header[4:12], uint64(interval.Nanoseconds()))
	if _, err := f.WriteAt(header, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing series header: %w", err)
	}

	meta := seriesMeta{
		Name:       key.Name,
		Labels:     key.Labels,
		IntervalNs: interval.Nanoseconds(),
		Capacity:   capacity,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(s.dir, hexHash+".meta.json"), metaBytes, 0o644); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing series metadata: %w", err)
	}

	newSeries := &series{
		key:      key,
		interval: interval,
		capacity: capacity,
		file:     f,
	}
	s.byKey[canonical] = newSeries
	s.byName[key.Name] = append(s.byName[key.Name], newSeries)
	return newSeries, nil
}

func (sr *series) write(t time.Time, value float64) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	slot := (t.UnixNano() / sr.interval.Nanoseconds()) % int64(sr.capacity)
	if slot < 0 {
		slot += int64(sr.capacity)
	}

	record := make([]byte, recordSize)
	binary.BigEndian.PutUint64(record[0:8], uint64(t.UnixNano()))
	binary.BigEndian.PutUint64(record[8:16], math.Float64bits(value))

	offset := int64(headerSize) + slot*recordSize
	_, err := sr.file.WriteAt(record, offset)
	return err
}

func (sr *series) readAll() ([]Point, error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	buf := make([]byte, sr.capacity*recordSize)
	if _, err := sr.file.ReadAt(buf, headerSize); err != nil {
		return nil, err
	}

	points := make([]Point, 0, sr.capacity)
	for i := 0; i < sr.capacity; i++ {
		off := i * recordSize
		ts := int64(binary.BigEndian.Uint64(buf[off : off+8]))
		if ts == 0 {
			continue // slot never written
		}
		value := math.Float64frombits(binary.BigEndian.Uint64(buf[off+8 : off+16]))
		points = append(points, Point{Time: time.Unix(0, ts), Value: value})
	}
	return points, nil
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

// Close releases every series' open file handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	for _, sr := range s.byKey {
		if err := sr.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
