package alert

import "time"

// TimestampedValue is one historical (time, value) pair — the only history
// a Condition ever needs (rate_of_change looks back Window; threshold and
// anomaly need none).
type TimestampedValue struct {
	Time  time.Time
	Value float64
}

// EvalContext is everything a Condition needs to decide whether it holds
// for the sample just ingested.
type EvalContext struct {
	Value     float64
	Time      time.Time
	Anomalous bool
	// History is oldest-first, pruned to the condition's own Window() —
	// empty for conditions that report Window() == 0.
	History []TimestampedValue
}

// Condition is one rule's trigger check (DESIGN/06 §6.1: "threshold,
// rate-of-change, or anomaly bit is set").
type Condition interface {
	Eval(ctx EvalContext) bool
	// Window reports how much history Eval needs; 0 means Eval only looks
	// at the current sample, so Engine skips history bookkeeping entirely.
	Window() time.Duration
}

// ThresholdCondition fires when the current value satisfies Operator Value.
type ThresholdCondition struct {
	Operator string
	Value    float64
}

func (c ThresholdCondition) Window() time.Duration { return 0 }

func (c ThresholdCondition) Eval(ctx EvalContext) bool {
	return compare(ctx.Value, c.Operator, c.Value)
}

// RateOfChangeCondition fires when the per-second rate of change over
// RateWindow satisfies Operator Value. Rate is (current - oldest-in-window)
// / elapsed-seconds, matching the common "rate()" convention rather than a
// raw delta, so Value is independent of how wide the window is configured.
type RateOfChangeCondition struct {
	Operator   string
	Value      float64
	RateWindow time.Duration
}

func (c RateOfChangeCondition) Window() time.Duration { return c.RateWindow }

func (c RateOfChangeCondition) Eval(ctx EvalContext) bool {
	if len(ctx.History) == 0 {
		return false
	}
	oldest := ctx.History[0]
	elapsed := ctx.Time.Sub(oldest.Time).Seconds()
	if elapsed <= 0 {
		return false // not enough history spanning the window yet
	}
	rate := (ctx.Value - oldest.Value) / elapsed
	return compare(rate, c.Operator, c.Value)
}

// AnomalyCondition fires whenever the sample's storage-embedded anomaly bit
// is set (DESIGN/06 §6.2) — requires features.ml on, or it never fires
// (config.Validate rejects this combination at load time).
type AnomalyCondition struct{}

func (c AnomalyCondition) Window() time.Duration     { return 0 }
func (c AnomalyCondition) Eval(ctx EvalContext) bool { return ctx.Anomalous }

func compare(a float64, op string, b float64) bool {
	switch op {
	case ">":
		return a > b
	case "<":
		return a < b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case "==":
		return a == b
	case "!=":
		return a != b
	default:
		return false
	}
}
