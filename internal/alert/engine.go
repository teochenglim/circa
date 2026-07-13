package alert

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
	"github.com/teochenglim/circa/internal/storage"
)

// ruleState tracks one (rule, series) pair's hysteresis and, once firing,
// enough to reconstruct its Alert without re-walking the rule list.
type ruleState struct {
	pendingSince time.Time // when the condition first started holding, zero if not currently holding
	firing       bool
	since        time.Time // when it actually started firing (pendingSince once "for" elapsed)
	value        float64
	labels       map[string]string
	metric       string
	ruleName     string
	severity     Severity
}

// Engine evaluates every configured Rule against each sample as it's
// ingested (DESIGN/06 §6.1: "same tick as ingestion, against tier-0 data"),
// and implements ingest.Consumer so it fans out from the same pipeline
// internal/storage does — see ARCHITECTURE.md's ingestion-event walkthrough.
type Engine struct {
	rules     []Rule
	notifiers map[string]Notifier
	logger    *slog.Logger

	mu      sync.Mutex
	history map[string][]TimestampedValue // keyed by rule.Name+"|"+seriesKey
	state   map[string]*ruleState
}

func New(rules []Rule, notifiers map[string]Notifier, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		rules:     rules,
		notifiers: notifiers,
		logger:    logger,
		history:   make(map[string][]TimestampedValue),
		state:     make(map[string]*ruleState),
	}
}

// Consume implements ingest.Consumer.
func (e *Engine) Consume(sample ingest.Sample) error {
	for _, rule := range e.rules {
		if rule.Metric != sample.Name {
			continue
		}
		if !labelsMatch(sample.Labels, rule.Labels) {
			continue
		}
		e.evaluate(rule, sample)
	}
	return nil
}

// labelsMatch reports whether sample's labels are a superset of match —
// same exact-match convention as the query API.
func labelsMatch(labels, match map[string]string) bool {
	for k, v := range match {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (e *Engine) evaluate(rule Rule, sample ingest.Sample) {
	seriesKey := storage.SeriesKey{Name: sample.Name, Labels: sample.Labels}.String()
	stateKey := rule.Name + "|" + seriesKey

	ctx := EvalContext{Value: sample.Value, Time: sample.Time, Anomalous: sample.Anomalous}
	if window := rule.Condition.Window(); window > 0 {
		ctx.History = e.updateHistory(stateKey, sample.Time, sample.Value, window)
	}
	holds := rule.Condition.Eval(ctx)

	e.mu.Lock()
	st := e.state[stateKey]
	if st == nil {
		st = &ruleState{}
		e.state[stateKey] = st
	}

	if !holds {
		wasFiring := st.firing
		*st = ruleState{}
		e.mu.Unlock()
		if wasFiring {
			e.dispatch(rule, sample, sample.Time, false)
		}
		return
	}

	if st.pendingSince.IsZero() {
		st.pendingSince = sample.Time
	}
	st.value = sample.Value
	st.labels = sample.Labels
	st.metric = sample.Name
	st.ruleName = rule.Name
	st.severity = rule.Severity

	shouldFire := !st.firing && sample.Time.Sub(st.pendingSince) >= rule.For
	since := st.since
	if shouldFire {
		st.firing = true
		st.since = st.pendingSince
		since = st.since
	}
	e.mu.Unlock()

	if shouldFire {
		e.dispatch(rule, sample, since, true)
	}
}

// updateHistory appends the current point, prunes anything older than
// window, and returns a copy (the caller reads it without holding e.mu).
func (e *Engine) updateHistory(stateKey string, t time.Time, value float64, window time.Duration) []TimestampedValue {
	e.mu.Lock()
	defer e.mu.Unlock()

	hist := append(e.history[stateKey], TimestampedValue{Time: t, Value: value})
	cutoff := t.Add(-window)
	i := 0
	for i < len(hist) && hist[i].Time.Before(cutoff) {
		i++
	}
	hist = hist[i:]
	e.history[stateKey] = hist

	return append([]TimestampedValue(nil), hist...)
}

// dispatch sends alert to every notifier rule.NotifyTo names (or every
// configured notifier if NotifyTo is empty), logging (not stopping on)
// failures — matches ingest.Pipeline's own "collect, don't block" pattern.
func (e *Engine) dispatch(rule Rule, sample ingest.Sample, since time.Time, firing bool) {
	a := Alert{
		RuleName: rule.Name,
		Metric:   sample.Name,
		Labels:   sample.Labels,
		Severity: rule.Severity,
		Value:    sample.Value,
		Since:    since,
		Firing:   firing,
	}

	targets := rule.NotifyTo
	if len(targets) == 0 {
		for name := range e.notifiers {
			targets = append(targets, name)
		}
	}
	for _, name := range targets {
		n, ok := e.notifiers[name]
		if !ok {
			continue
		}
		if err := n.Notify(a); err != nil {
			e.logger.Warn("alert notify failed", "notifier", name, "rule", rule.Name, "error", err)
		}
	}
}

// Alerts returns every currently-firing alert, oldest-firing first — for
// GET /api/v1/alerts and the dashboard's Alerts panel.
func (e *Engine) Alerts() []Alert {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []Alert
	for _, st := range e.state {
		if !st.firing {
			continue
		}
		out = append(out, Alert{
			RuleName: st.ruleName,
			Metric:   st.metric,
			Labels:   st.labels,
			Severity: st.severity,
			Value:    st.value,
			Since:    st.since,
			Firing:   true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}
