package alert

import (
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/config"
)

func TestNewRuleParsesEachConditionType(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.ConditionConfig
		wantErr bool
	}{
		{"threshold", config.ConditionConfig{Type: "threshold", Operator: ">", Value: 90}, false},
		{"rate_of_change", config.ConditionConfig{Type: "rate_of_change", Operator: ">", Value: 5, Window: config.Duration(time.Minute)}, false},
		{"anomaly", config.ConditionConfig{Type: "anomaly"}, false},
		{"unknown", config.ConditionConfig{Type: "bogus"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := NewRule(config.AlertRule{Name: "r", Metric: "m", Condition: tc.cfg, Severity: "warning"})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewRule: %v", err)
			}
			if rule.Condition == nil {
				t.Fatal("expected a non-nil Condition")
			}
		})
	}
}

func TestNewRuleDefaultsSeverityToWarning(t *testing.T) {
	rule, err := NewRule(config.AlertRule{
		Name:      "r",
		Metric:    "m",
		Condition: config.ConditionConfig{Type: "threshold", Operator: ">", Value: 0},
	})
	if err != nil {
		t.Fatalf("NewRule: %v", err)
	}
	if rule.Severity != SeverityWarning {
		t.Errorf("Severity = %q, want %q", rule.Severity, SeverityWarning)
	}
}

func TestCompareOperators(t *testing.T) {
	cases := []struct {
		a, b float64
		op   string
		want bool
	}{
		{5, 3, ">", true}, {3, 5, ">", false},
		{3, 5, "<", true}, {5, 3, "<", false},
		{5, 5, ">=", true}, {5, 6, ">=", false},
		{5, 5, "<=", true}, {6, 5, "<=", false},
		{5, 5, "==", true}, {5, 6, "==", false},
		{5, 6, "!=", true}, {5, 5, "!=", false},
		{5, 5, "bogus", false},
	}
	for _, tc := range cases {
		if got := compare(tc.a, tc.op, tc.b); got != tc.want {
			t.Errorf("compare(%v, %q, %v) = %v, want %v", tc.a, tc.op, tc.b, got, tc.want)
		}
	}
}
