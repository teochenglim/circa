package storage

import "math"

// EncodeAnomalyBit and DecodeAnomalyBit implement Netdata's trick (DESIGN/06
// §6.2): steal the least-significant mantissa bit of the stored float64 to
// carry the anomaly flag, instead of a separate time series. The numeric
// perturbation is at most 1 part in 2^52 (relative error ~2e-16) — far below
// any real metric's meaningful precision, with one edge case worth knowing:
// encoding an exact 0 as anomalous produces the smallest subnormal float64
// (~4.9e-324), not 0, so an exact `value == 0` comparison downstream would
// no longer match. Acceptable for a monitoring/alerting context; not
// appropriate if a series' exact bit pattern needs to round-trip losslessly.
func EncodeAnomalyBit(value float64, anomalous bool) float64 {
	bits := math.Float64bits(value)
	if anomalous {
		bits |= 1
	} else {
		bits &^= 1
	}
	return math.Float64frombits(bits)
}

// DecodeAnomalyBit reports whether value's LSB (as set by EncodeAnomalyBit)
// marks it anomalous. The value itself needs no further adjustment — it's
// already bit-for-bit what was stored.
func DecodeAnomalyBit(value float64) bool {
	return math.Float64bits(value)&1 == 1
}
