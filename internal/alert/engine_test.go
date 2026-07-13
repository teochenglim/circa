package alert

import (
	"sync"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

type fakeNotifier struct {
	mu     sync.Mutex
	alerts []Alert
}

func (f *fakeNotifier) Notify(a Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = append(f.alerts, a)
	return nil
}

func (f *fakeNotifier) received() []Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Alert(nil), f.alerts...)
}

func sample(name string, labels map[string]string, t time.Time, value float64, anomalous bool) ingest.Sample {
	return ingest.Sample{Name: name, Labels: labels, Time: t, Value: value, Anomalous: anomalous}
}

func TestThresholdFiresAfterForElapses(t *testing.T) {
	notifier := &fakeNotifier{}
	rule := Rule{
		Name:      "high_cpu",
		Metric:    "cpu",
		Condition: ThresholdCondition{Operator: ">", Value: 90},
		For:       30 * time.Second,
		Severity:  SeverityWarning,
	}
	engine := New([]Rule{rule}, map[string]Notifier{"n": notifier}, nil)

	base := time.Now()
	// First breach: shouldn't fire yet, "for" hasn't elapsed.
	engine.Consume(sample("cpu", nil, base, 95, false))
	if got := len(engine.Alerts()); got != 0 {
		t.Fatalf("alerts after first breach = %d, want 0", got)
	}

	// 31s later, still breaching: should now fire.
	engine.Consume(sample("cpu", nil, base.Add(31*time.Second), 96, false))
	alerts := engine.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("alerts after for elapses = %d, want 1", len(alerts))
	}
	if !alerts[0].Firing || alerts[0].RuleName != "high_cpu" {
		t.Errorf("unexpected alert: %+v", alerts[0])
	}

	received := notifier.received()
	if len(received) != 1 || !received[0].Firing {
		t.Fatalf("expected exactly one firing notification, got %+v", received)
	}

	// Value drops below threshold: should resolve.
	engine.Consume(sample("cpu", nil, base.Add(32*time.Second), 10, false))
	if got := len(engine.Alerts()); got != 0 {
		t.Fatalf("alerts after resolve = %d, want 0", got)
	}
	received = notifier.received()
	if len(received) != 2 || received[1].Firing {
		t.Fatalf("expected a resolved notification second, got %+v", received)
	}
}

func TestThresholdResetsOnDipBeforeForElapses(t *testing.T) {
	notifier := &fakeNotifier{}
	rule := Rule{
		Name:      "high_cpu",
		Metric:    "cpu",
		Condition: ThresholdCondition{Operator: ">", Value: 90},
		For:       30 * time.Second,
	}
	engine := New([]Rule{rule}, map[string]Notifier{"n": notifier}, nil)

	base := time.Now()
	engine.Consume(sample("cpu", nil, base, 95, false))
	// Dips below threshold before 30s elapsed - pending state should reset.
	engine.Consume(sample("cpu", nil, base.Add(10*time.Second), 50, false))
	// Breaches again, but only 20s since the reset point - still shouldn't fire.
	engine.Consume(sample("cpu", nil, base.Add(30*time.Second), 95, false))
	if got := len(engine.Alerts()); got != 0 {
		t.Fatalf("alerts = %d, want 0 (hysteresis should have reset on the dip)", got)
	}
}

func TestRuleOnlyMatchesItsMetricAndLabels(t *testing.T) {
	rule := Rule{
		Name:      "high_cpu",
		Metric:    "cpu",
		Labels:    map[string]string{"host": "a"},
		Condition: ThresholdCondition{Operator: ">", Value: 0},
	}
	engine := New([]Rule{rule}, nil, nil)

	now := time.Now()
	engine.Consume(sample("memory", map[string]string{"host": "a"}, now, 100, false))
	engine.Consume(sample("cpu", map[string]string{"host": "b"}, now, 100, false))
	if got := len(engine.Alerts()); got != 0 {
		t.Fatalf("alerts = %d, want 0 (wrong metric/labels shouldn't match)", got)
	}

	engine.Consume(sample("cpu", map[string]string{"host": "a", "extra": "label"}, now, 100, false))
	if got := len(engine.Alerts()); got != 1 {
		t.Fatalf("alerts = %d, want 1 (labels are a superset match)", got)
	}
}

func TestRateOfChangeCondition(t *testing.T) {
	rule := Rule{
		Name:      "fast_growth",
		Metric:    "requests",
		Condition: RateOfChangeCondition{Operator: ">", Value: 5, RateWindow: time.Minute},
	}
	engine := New([]Rule{rule}, nil, nil)

	base := time.Now()
	engine.Consume(sample("requests", nil, base, 0, false))
	// 60s later, +600: rate = 10/s > 5/s - should fire (for=0, fires immediately).
	engine.Consume(sample("requests", nil, base.Add(60*time.Second), 600, false))
	if got := len(engine.Alerts()); got != 1 {
		t.Fatalf("alerts = %d, want 1", got)
	}
}

func TestAnomalyCondition(t *testing.T) {
	rule := Rule{
		Name:      "unusual",
		Metric:    "latency",
		Condition: AnomalyCondition{},
	}
	engine := New([]Rule{rule}, nil, nil)

	now := time.Now()
	engine.Consume(sample("latency", nil, now, 42, false))
	if got := len(engine.Alerts()); got != 0 {
		t.Fatalf("alerts = %d, want 0 for non-anomalous sample", got)
	}
	engine.Consume(sample("latency", nil, now.Add(time.Second), 999, true))
	if got := len(engine.Alerts()); got != 1 {
		t.Fatalf("alerts = %d, want 1 for anomalous sample", got)
	}
}

func TestDispatchOnlyToNamedNotifiers(t *testing.T) {
	a := &fakeNotifier{}
	b := &fakeNotifier{}
	rule := Rule{
		Name:      "r",
		Metric:    "m",
		Condition: ThresholdCondition{Operator: ">", Value: 0},
		NotifyTo:  []string{"a"},
	}
	engine := New([]Rule{rule}, map[string]Notifier{"a": a, "b": b}, nil)
	engine.Consume(sample("m", nil, time.Now(), 1, false))

	if len(a.received()) != 1 {
		t.Errorf("notifier a should have received 1 alert, got %d", len(a.received()))
	}
	if len(b.received()) != 0 {
		t.Errorf("notifier b should have received 0 alerts (not named in notify), got %d", len(b.received()))
	}
}

func TestDispatchToAllNotifiersWhenNotifyEmpty(t *testing.T) {
	a := &fakeNotifier{}
	b := &fakeNotifier{}
	rule := Rule{Name: "r", Metric: "m", Condition: ThresholdCondition{Operator: ">", Value: 0}}
	engine := New([]Rule{rule}, map[string]Notifier{"a": a, "b": b}, nil)
	engine.Consume(sample("m", nil, time.Now(), 1, false))

	if len(a.received()) != 1 || len(b.received()) != 1 {
		t.Errorf("expected both notifiers to receive the alert, got a=%d b=%d", len(a.received()), len(b.received()))
	}
}
