package storage

import (
	"math"
	"testing"
	"time"
)

func TestEncodeDecodeAnomalyBitRoundTrips(t *testing.T) {
	// math.SmallestNonzeroFloat64 is deliberately excluded from the
	// relative-error check below: it's the one value class (subnormals,
	// where flipping the LSB is a 100% relative change) where the
	// documented "negligible perturbation" tradeoff doesn't hold — expected
	// per anomalybit.go's own doc comment, not a bug.
	values := []float64{0, 1, -1, 3.14159, -273.15, 1e300, -1e-300, math.MaxFloat64, math.SmallestNonzeroFloat64}
	for _, v := range values {
		for _, anomalous := range []bool{true, false} {
			encoded := EncodeAnomalyBit(v, anomalous)
			if got := DecodeAnomalyBit(encoded); got != anomalous {
				t.Errorf("value %v anomalous=%v: decoded %v", v, anomalous, got)
			}
			if v != 0 && math.Abs(v) > 1e-100 {
				relErr := math.Abs(encoded-v) / math.Abs(v)
				if relErr > 1e-10 {
					t.Errorf("value %v: relative error %v too large after encoding", v, relErr)
				}
			}
		}
	}
}

func TestEncodeAnomalyBitClearsWhenFalse(t *testing.T) {
	// An already-odd-mantissa value must have its LSB explicitly cleared
	// when encoded as not-anomalous, not left however it happened to be.
	oddBits := math.Float64bits(1.0) | 1
	odd := math.Float64frombits(oddBits)
	encoded := EncodeAnomalyBit(odd, false)
	if DecodeAnomalyBit(encoded) {
		t.Error("expected anomalous=false to clear the LSB even on odd input")
	}
}

func TestStoreRoundTripsAnomalyBit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := SeriesKey{Name: "cpu", Labels: map[string]string{"host": "a"}}
	now := time.Now().Truncate(time.Second)

	if err := s.Append(key, time.Second, now, 42, true); err != nil {
		t.Fatalf("Append anomalous: %v", err)
	}
	if err := s.Append(key, time.Second, now.Add(time.Second), 43, false); err != nil {
		t.Fatalf("Append normal: %v", err)
	}

	results, err := s.QueryRange("cpu", nil, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(results) != 1 || len(results[0].Points) != 2 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if !results[0].Points[0].Anomalous {
		t.Error("first point should be anomalous")
	}
	if results[0].Points[1].Anomalous {
		t.Error("second point should not be anomalous")
	}
	if math.Round(results[0].Points[0].Value) != 42 || math.Round(results[0].Points[1].Value) != 43 {
		t.Errorf("values not preserved (within rounding): %v, %v", results[0].Points[0].Value, results[0].Points[1].Value)
	}
}
