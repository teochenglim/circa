// Package alert implements circa's rule engine (DESIGN/06 §6.1): a rule is a
// metric selector plus a Condition, evaluated on the same tick as ingestion
// against the raw sample just ingested, with a duration-based hysteresis
// window before it actually fires. Notifier is a pluggable dispatch
// interface; concrete channels (webhook, Slack) live in internal/alert/notify
// so adding one never touches the evaluation logic here (ARCHITECTURE.md
// "Adding a new alert notifier").
package alert

import "time"

// Severity is a rule's configured severity, echoed back on every Alert.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Alert is one rule's current verdict for one matching series — sent to
// notifiers on every firing/resolved transition, and returned by
// Engine.Alerts for the API/UI.
type Alert struct {
	RuleName string            `json:"rule"`
	Metric   string            `json:"metric"`
	Labels   map[string]string `json:"labels,omitempty"`
	Severity Severity          `json:"severity"`
	Value    float64           `json:"value"`
	Since    time.Time         `json:"since"`
	Firing   bool              `json:"firing"`
}

// Notifier dispatches an Alert to some external channel. Concrete
// implementations (webhook, Slack) live in internal/alert/notify.
type Notifier interface {
	Notify(Alert) error
}
