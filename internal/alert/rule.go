package alert

import (
	"fmt"
	"time"

	"github.com/teochenglim/circa/internal/config"
)

// Rule is one alerting rule, ready to evaluate — the internal counterpart
// to config.AlertRule, with Condition already parsed.
type Rule struct {
	Name      string
	Metric    string
	Labels    map[string]string
	Condition Condition
	For       time.Duration
	Severity  Severity
	NotifyTo  []string // notifier names; empty = every configured notifier
}

// NewRule converts a config.AlertRule into a Rule. Called once at startup,
// after config.Load has already run Config.Validate — which guarantees
// Condition.Type/Operator are one of the recognized values — so the error
// path here is defensive, not expected to trigger in practice.
func NewRule(cfg config.AlertRule) (Rule, error) {
	cond, err := newCondition(cfg.Condition)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: %w", cfg.Name, err)
	}
	severity := Severity(cfg.Severity)
	if severity == "" {
		severity = SeverityWarning
	}
	return Rule{
		Name:      cfg.Name,
		Metric:    cfg.Metric,
		Labels:    cfg.Labels,
		Condition: cond,
		For:       time.Duration(cfg.For),
		Severity:  severity,
		NotifyTo:  cfg.Notify,
	}, nil
}

func newCondition(cfg config.ConditionConfig) (Condition, error) {
	switch cfg.Type {
	case "threshold":
		return ThresholdCondition{Operator: cfg.Operator, Value: cfg.Value}, nil
	case "rate_of_change":
		return RateOfChangeCondition{Operator: cfg.Operator, Value: cfg.Value, RateWindow: time.Duration(cfg.Window)}, nil
	case "anomaly":
		return AnomalyCondition{}, nil
	default:
		return nil, fmt.Errorf("unknown condition type %q", cfg.Type)
	}
}
