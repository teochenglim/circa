// Multi-tier downsampling per DESIGN/03 §3.4: as tier-0 points arrive, roll
// them up into fixed-interval min/avg/max buckets for tier-1 (minute) and
// tier-2 (hour), so a caller can chart a wide time range without reading
// (and discarding most of) months of raw points.
//
// A rollup's min/avg/max triplet is stored as three ordinary series in the
// tier's own Store, named "<metric>#min"/"#avg"/"#max" — reusing the exact
// same chunked/compressed engine as tier-0 rather than inventing a second,
// three-values-per-point chunk format. The tradeoff is an extra ~2x
// timestamp-stream overhead versus a hypothetical shared-timestamp design
// (three delta-of-delta streams instead of one); simplicity and reuse of a
// single well-tested engine won out for this milestone. See RELEASE/v0.2.0.md.
package storage

import (
	"sync"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

// Tier selects which resolution a query reads from.
type Tier int

const (
	TierRaw Tier = iota
	TierMinute
	TierHour
)

const (
	minuteInterval = time.Minute
	hourInterval   = time.Hour
)

// AggPoint is one rolled-up bucket: the min/avg/max of every raw point that
// landed in it.
type AggPoint struct {
	Time          time.Time
	Min, Avg, Max float64
}

// AggSeriesResult is one series' rolled-up points matching a query.
type AggSeriesResult struct {
	Key    SeriesKey
	Points []AggPoint
}

type bucketAccumulator struct {
	bucketStart   int64 // unix seconds, start of the currently-open bucket
	count         int
	sum, min, max float64
}

func (b *bucketAccumulator) add(value float64) {
	if b.count == 0 {
		b.min, b.max = value, value
	} else {
		if value < b.min {
			b.min = value
		}
		if value > b.max {
			b.max = value
		}
	}
	b.sum += value
	b.count++
}

func (b *bucketAccumulator) avg() float64 {
	return b.sum / float64(b.count)
}

// TieredStore wires a raw store together with minute/hour rollup stores.
// Either rollup tier may be nil (retention 0 disables it); Raw is always
// present.
type TieredStore struct {
	Raw    *Store
	Minute *Store
	Hour   *Store

	mu        sync.Mutex
	minuteAcc map[string]*bucketAccumulator
	hourAcc   map[string]*bucketAccumulator
}

// OpenTiered opens (creating if needed) the raw/minute/hour tiers under
// dir/raw, dir/minute, dir/hour. minuteRetention/hourRetention of 0 disables
// that tier (no subdirectory is created, no rollups computed).
func OpenTiered(dir string, rawRetention, minuteRetention, hourRetention time.Duration) (*TieredStore, error) {
	raw, err := Open(dir+"/raw", rawRetention)
	if err != nil {
		return nil, err
	}

	ts := &TieredStore{Raw: raw}

	if minuteRetention > 0 {
		minute, err := Open(dir+"/minute", minuteRetention)
		if err != nil {
			return nil, err
		}
		ts.Minute = minute
		ts.minuteAcc = make(map[string]*bucketAccumulator)
	}
	if hourRetention > 0 {
		hour, err := Open(dir+"/hour", hourRetention)
		if err != nil {
			return nil, err
		}
		ts.Hour = hour
		ts.hourAcc = make(map[string]*bucketAccumulator)
	}

	return ts, nil
}

// Consume implements ingest.Consumer: every sample is written to the raw
// tier, then folded into the open minute/hour bucket for its series,
// flushing the previous bucket's min/avg/max into that tier's store when
// time has moved into a new bucket.
func (ts *TieredStore) Consume(sample ingest.Sample) error {
	interval := sample.Interval
	if interval <= 0 {
		interval = time.Second
	}
	key := SeriesKey{Name: sample.Name, Labels: sample.Labels}

	if err := ts.Raw.Append(key, interval, sample.Time, sample.Value); err != nil {
		return err
	}

	if ts.Minute != nil {
		if err := ts.rollup(ts.Minute, ts.minuteAcc, key, minuteInterval, sample.Time, sample.Value); err != nil {
			return err
		}
	}
	if ts.Hour != nil {
		if err := ts.rollup(ts.Hour, ts.hourAcc, key, hourInterval, sample.Time, sample.Value); err != nil {
			return err
		}
	}
	return nil
}

func (ts *TieredStore) rollup(store *Store, accs map[string]*bucketAccumulator, key SeriesKey, bucketWidth time.Duration, t time.Time, value float64) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	canonical := key.String()
	bucketWidthSec := int64(bucketWidth / time.Second)
	bucketStart := (t.Unix() / bucketWidthSec) * bucketWidthSec

	acc := accs[canonical]
	if acc == nil {
		accs[canonical] = &bucketAccumulator{bucketStart: bucketStart}
		accs[canonical].add(value)
		return nil
	}

	if bucketStart == acc.bucketStart {
		acc.add(value)
		return nil
	}

	// Time has moved into a new bucket: flush the closed one, then start a
	// fresh accumulator for the new bucket.
	flushTime := time.Unix(acc.bucketStart, 0)
	if err := appendAgg(store, key, bucketWidth, flushTime, acc); err != nil {
		return err
	}

	accs[canonical] = &bucketAccumulator{bucketStart: bucketStart}
	accs[canonical].add(value)
	return nil
}

func appendAgg(store *Store, key SeriesKey, interval time.Duration, t time.Time, acc *bucketAccumulator) error {
	minKey := SeriesKey{Name: key.Name + "#min", Labels: key.Labels}
	avgKey := SeriesKey{Name: key.Name + "#avg", Labels: key.Labels}
	maxKey := SeriesKey{Name: key.Name + "#max", Labels: key.Labels}

	if err := store.Append(minKey, interval, t, acc.min); err != nil {
		return err
	}
	if err := store.Append(avgKey, interval, t, acc.avg()); err != nil {
		return err
	}
	return store.Append(maxKey, interval, t, acc.max)
}

// QueryRange reads tier for name (optionally narrowed by match) in
// [start, end]. TierRaw returns single-valued points; TierMinute/TierHour
// return min/avg/max triplets zipped from that tier's three sub-series.
func (ts *TieredStore) QueryRange(name string, match map[string]string, tier Tier, start, end time.Time) ([]SeriesResult, []AggSeriesResult, error) {
	switch tier {
	case TierRaw:
		results, err := ts.Raw.QueryRange(name, match, start, end)
		return results, nil, err
	case TierMinute:
		results, err := queryAgg(ts.Minute, name, match, start, end)
		return nil, results, err
	case TierHour:
		results, err := queryAgg(ts.Hour, name, match, start, end)
		return nil, results, err
	default:
		return nil, nil, nil
	}
}

func queryAgg(store *Store, name string, match map[string]string, start, end time.Time) ([]AggSeriesResult, error) {
	if store == nil {
		return nil, nil
	}

	minResults, err := store.QueryRange(name+"#min", match, start, end)
	if err != nil {
		return nil, err
	}
	avgResults, err := store.QueryRange(name+"#avg", match, start, end)
	if err != nil {
		return nil, err
	}
	maxResults, err := store.QueryRange(name+"#max", match, start, end)
	if err != nil {
		return nil, err
	}

	avgByLabels := map[string]SeriesResult{}
	for _, r := range avgResults {
		avgByLabels[labelsKey(r.Key.Labels)] = r
	}
	maxByLabels := map[string]SeriesResult{}
	for _, r := range maxResults {
		maxByLabels[labelsKey(r.Key.Labels)] = r
	}

	results := make([]AggSeriesResult, 0, len(minResults))
	for _, minRes := range minResults {
		lk := labelsKey(minRes.Key.Labels)
		avgRes := avgByLabels[lk]
		maxRes := maxByLabels[lk]

		n := len(minRes.Points)
		if len(avgRes.Points) < n {
			n = len(avgRes.Points)
		}
		if len(maxRes.Points) < n {
			n = len(maxRes.Points)
		}

		points := make([]AggPoint, n)
		for i := 0; i < n; i++ {
			points[i] = AggPoint{
				Time: minRes.Points[i].Time,
				Min:  minRes.Points[i].Value,
				Avg:  avgRes.Points[i].Value,
				Max:  maxRes.Points[i].Value,
			}
		}

		results = append(results, AggSeriesResult{
			Key:    SeriesKey{Name: name, Labels: minRes.Key.Labels},
			Points: points,
		})
	}
	return results, nil
}

func labelsKey(labels map[string]string) string {
	return SeriesKey{Labels: labels}.String()
}

// Series lists every real (non-rollup) series known to the raw tier.
func (ts *TieredStore) Series() []SeriesKey {
	return ts.Raw.Series()
}

// Close releases every tier's resources.
func (ts *TieredStore) Close() error {
	if err := ts.Raw.Close(); err != nil {
		return err
	}
	if ts.Minute != nil {
		if err := ts.Minute.Close(); err != nil {
			return err
		}
	}
	if ts.Hour != nil {
		return ts.Hour.Close()
	}
	return nil
}
