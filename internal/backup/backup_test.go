package backup

import "testing"

func TestLabelsJSON(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"nil map", nil, "{}"},
		{"empty map", map[string]string{}, "{}"},
		{"one label", map[string]string{"cpu": "0"}, `{"cpu":"0"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := labelsJSON(c.labels); got != c.want {
				t.Errorf("labelsJSON(%v) = %q, want %q", c.labels, got, c.want)
			}
		})
	}
}
