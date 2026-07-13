// Gorilla-style delta-of-delta timestamp + XOR value encoding, per
// DESIGN/03 §3.2 (Facebook's "Gorilla" paper). Operates on a chunk (a bounded
// run of points for one series) at a time: the whole chunk is re-encoded from
// scratch on every append (see chunk.go) rather than incrementally, which
// keeps this package a pure, stateless encode(points)/decode(bytes) pair
// that's simple to test in isolation.
//
// Timestamps are truncated to whole seconds before encoding — the bit-width
// buckets below assume small, mostly-repeating deltas (as node_exporter/
// Prometheus-style scrape intervals produce in seconds), and sub-second
// precision isn't meaningful for the raw/minute/hour tiers this store
// serves.
package storage

import (
	"fmt"
	"math"
	"math/bits"
)

type chunkPoint struct {
	Sec   int64
	Value float64
}

// encodeChunk bit-packs points (must be in non-decreasing Sec order) into a
// compact byte slice: raw first timestamp+value, then delta-of-delta
// timestamps and XOR'd values for every point after that.
func encodeChunk(points []chunkPoint) []byte {
	w := &bitWriter{}
	if len(points) == 0 {
		return w.bytes()
	}

	w.writeBits(uint64(points[0].Sec), 64)
	w.writeBits(math.Float64bits(points[0].Value), 64)
	if len(points) == 1 {
		return w.bytes()
	}

	prevDelta := points[1].Sec - points[0].Sec
	writeSigned(w, prevDelta, 32)

	window := &xorWindow{}
	prevValueBits := math.Float64bits(points[0].Value)
	writeXOR(w, prevValueBits, math.Float64bits(points[1].Value), window)
	prevValueBits = math.Float64bits(points[1].Value)
	prevSec := points[1].Sec

	for i := 2; i < len(points); i++ {
		delta := points[i].Sec - prevSec
		dod := delta - prevDelta
		writeDeltaOfDelta(w, dod)
		prevDelta = delta
		prevSec = points[i].Sec

		valueBits := math.Float64bits(points[i].Value)
		writeXOR(w, prevValueBits, valueBits, window)
		prevValueBits = valueBits
	}

	return w.bytes()
}

// decodeChunk reverses encodeChunk. n is the number of points encoded
// (tracked alongside the chunk, since the bitstream itself has no length
// prefix). Malformed input is reported as an error rather than panicking,
// since chunk bytes are read back from disk.
func decodeChunk(data []byte, n int) (points []chunkPoint, err error) {
	if n == 0 {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			points, err = nil, fmt.Errorf("decoding chunk: %v", r)
		}
	}()

	r := newBitReader(data)
	points = make([]chunkPoint, 0, n)

	sec := int64(r.readBits(64))
	value := math.Float64frombits(r.readBits(64))
	points = append(points, chunkPoint{Sec: sec, Value: value})
	if n == 1 {
		if r.overrun {
			return nil, fmt.Errorf("decoding chunk: truncated bitstream (expected %d points)", n)
		}
		return points, nil
	}

	delta := readSigned(r, 32)
	sec += delta
	window := &xorWindow{}
	valueBits := readXOR(r, math.Float64bits(value), window)
	value = math.Float64frombits(valueBits)
	points = append(points, chunkPoint{Sec: sec, Value: value})

	for i := 2; i < n; i++ {
		dod := readDeltaOfDelta(r)
		delta += dod
		sec += delta

		valueBits = readXOR(r, valueBits, window)
		value = math.Float64frombits(valueBits)
		points = append(points, chunkPoint{Sec: sec, Value: value})
	}

	if r.overrun {
		return nil, fmt.Errorf("decoding chunk: truncated bitstream (expected %d points)", n)
	}
	return points, nil
}

func writeSigned(w *bitWriter, v int64, nBits int) {
	w.writeBits(uint64(v)&((1<<uint(nBits))-1), nBits)
}

func readSigned(r *bitReader, nBits int) int64 {
	v := r.readBits(nBits)
	signBit := uint64(1) << uint(nBits-1)
	if v&signBit != 0 {
		return int64(v) - (int64(1) << uint(nBits))
	}
	return int64(v)
}

// writeDeltaOfDelta implements the classic Gorilla bucket scheme: smaller,
// more-likely deltas cost fewer bits. A dod of 0 (perfectly regular
// interval) costs a single bit.
func writeDeltaOfDelta(w *bitWriter, dod int64) {
	switch {
	case dod == 0:
		w.writeBit(false)
	case -63 <= dod && dod <= 64:
		w.writeBits(0b10, 2)
		writeSigned(w, dod, 7)
	case -255 <= dod && dod <= 256:
		w.writeBits(0b110, 3)
		writeSigned(w, dod, 9)
	case -2047 <= dod && dod <= 2048:
		w.writeBits(0b1110, 4)
		writeSigned(w, dod, 12)
	default:
		w.writeBits(0b1111, 4)
		writeSigned(w, dod, 32)
	}
}

func readDeltaOfDelta(r *bitReader) int64 {
	if !r.readBit() {
		return 0
	}
	if !r.readBit() {
		return readSigned(r, 7)
	}
	if !r.readBit() {
		return readSigned(r, 9)
	}
	if !r.readBit() {
		return readSigned(r, 12)
	}
	return readSigned(r, 32)
}

// xorWindow tracks the previous XOR's leading/trailing zero counts, so a new
// XOR whose meaningful bits fit inside that same window can be written
// without repeating the (leading, length) header.
type xorWindow struct {
	leading, trailing int
	set               bool
}

func writeXOR(w *bitWriter, prev, cur uint64, win *xorWindow) {
	xor := prev ^ cur
	if xor == 0 {
		w.writeBit(false)
		return
	}
	w.writeBit(true)

	leading := bits.LeadingZeros64(xor)
	trailing := bits.TrailingZeros64(xor)

	if win.set && leading >= win.leading && trailing >= win.trailing {
		w.writeBit(false)
		meaningful := 64 - win.leading - win.trailing
		w.writeBits((xor>>uint(win.trailing))&((1<<uint(meaningful))-1), meaningful)
		return
	}

	w.writeBit(true)
	if leading > 31 {
		leading = 31 // clamp per the standard Gorilla encoding (5-bit field)
	}
	meaningful := 64 - leading - trailing
	w.writeBits(uint64(leading), 5)
	w.writeBits(uint64(meaningful-1), 6)
	w.writeBits((xor>>uint(trailing))&((1<<uint(meaningful))-1), meaningful)

	win.leading, win.trailing, win.set = leading, trailing, true
}

func readXOR(r *bitReader, prev uint64, win *xorWindow) uint64 {
	if !r.readBit() {
		return prev
	}

	var leading, trailing, meaningful int
	if !r.readBit() {
		leading, trailing = win.leading, win.trailing
		meaningful = 64 - leading - trailing
	} else {
		leading = int(r.readBits(5))
		meaningful = int(r.readBits(6)) + 1
		trailing = 64 - leading - meaningful
		win.leading, win.trailing, win.set = leading, trailing, true
	}

	bitsVal := r.readBits(meaningful)
	xor := bitsVal << uint(trailing)
	return prev ^ xor
}
