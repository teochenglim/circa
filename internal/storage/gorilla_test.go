package storage

import (
	"math/rand"
	"testing"
)

func TestEncodeDecodeChunkRoundTrip(t *testing.T) {
	cases := [][]chunkPoint{
		nil,
		{{Sec: 1000, Value: 1.5}},
		{{Sec: 1000, Value: 1.5}, {Sec: 1015, Value: 1.5}},
		{{Sec: 1000, Value: 1.5}, {Sec: 1015, Value: 2.5}, {Sec: 1030, Value: 2.5}, {Sec: 1045, Value: 3.75}},
	}

	for i, points := range cases {
		encoded := encodeChunk(points)
		decoded, err := decodeChunk(encoded, len(points))
		if err != nil {
			t.Fatalf("case %d: decode error: %v", i, err)
		}
		if len(decoded) != len(points) {
			t.Fatalf("case %d: got %d points, want %d", i, len(decoded), len(points))
		}
		for j := range points {
			if decoded[j] != points[j] {
				t.Errorf("case %d point %d: got %+v, want %+v", i, j, decoded[j], points[j])
			}
		}
	}
}

func TestEncodeDecodeChunkRegularInterval(t *testing.T) {
	const n = 500
	points := make([]chunkPoint, n)
	sec := int64(1_700_000_000)
	for i := range points {
		points[i] = chunkPoint{Sec: sec, Value: 42.0}
		sec += 15
	}

	encoded := encodeChunk(points)
	decoded, err := decodeChunk(encoded, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range points {
		if decoded[i] != points[i] {
			t.Fatalf("point %d: got %+v, want %+v", i, decoded[i], points[i])
		}
	}

	rawSize := n * 16
	if len(encoded) >= rawSize {
		t.Errorf("expected compression for a constant-value regular-interval series: encoded %d bytes >= raw %d bytes", len(encoded), rawSize)
	}
	t.Logf("constant series: %d points, raw=%d bytes, encoded=%d bytes (%.1fx)", n, rawSize, len(encoded), float64(rawSize)/float64(len(encoded)))
}

func TestEncodeDecodeChunkVaryingValues(t *testing.T) {
	const n = 500
	points := make([]chunkPoint, n)
	sec := int64(1_700_000_000)
	rng := rand.New(rand.NewSource(1))
	value := 50.0
	for i := range points {
		value += rng.Float64()*2 - 1 // small random walk, like a CPU% gauge
		points[i] = chunkPoint{Sec: sec, Value: value}
		sec += 15
	}

	encoded := encodeChunk(points)
	decoded, err := decodeChunk(encoded, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != n {
		t.Fatalf("got %d points, want %d", len(decoded), n)
	}
	for i := range points {
		if decoded[i] != points[i] {
			t.Fatalf("point %d: got %+v, want %+v", i, decoded[i], points[i])
		}
	}
}

func TestEncodeDecodeChunkIrregularInterval(t *testing.T) {
	points := []chunkPoint{
		{Sec: 1000, Value: 1},
		{Sec: 1015, Value: 2},
		{Sec: 1017, Value: 3}, // interval jitter
		{Sec: 1200, Value: 4}, // big gap
		{Sec: 1201, Value: 5},
	}
	encoded := encodeChunk(points)
	decoded, err := decodeChunk(encoded, len(points))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range points {
		if decoded[i] != points[i] {
			t.Fatalf("point %d: got %+v, want %+v", i, decoded[i], points[i])
		}
	}
}

func TestEncodeDecodeChunkNegativeAndSpecialValues(t *testing.T) {
	points := []chunkPoint{
		{Sec: 1000, Value: -5.5},
		{Sec: 1015, Value: 0},
		{Sec: 1030, Value: -0.000001},
		{Sec: 1045, Value: 1e18},
		{Sec: 1060, Value: -1e18},
	}
	encoded := encodeChunk(points)
	decoded, err := decodeChunk(encoded, len(points))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range points {
		if decoded[i] != points[i] {
			t.Fatalf("point %d: got %+v, want %+v", i, decoded[i], points[i])
		}
	}
}

func TestDecodeChunkCorruptDataReturnsErrorNotPanic(t *testing.T) {
	// Truncated buffer: claims 10 points but has only a few bytes.
	_, err := decodeChunk([]byte{0x01, 0x02, 0x03}, 10)
	if err == nil {
		t.Fatal("expected an error for truncated/corrupt chunk data, got nil")
	}
}
