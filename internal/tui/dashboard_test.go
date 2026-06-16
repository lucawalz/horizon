package tui

import "testing"

func TestGaugeColorTiers(t *testing.T) {
	const threshold = 0.8
	cases := []struct {
		name  string
		score float64
		want  string
	}{
		{"below warn ratio is green", 0.1, dotGreen},
		{"just above warn ratio is yellow", threshold*warnThresholdRatio + 0.01, dotYellow},
		{"between warn and threshold is yellow", 0.7, dotYellow},
		{"at threshold is red", threshold, dotRed},
		{"above threshold is red", 0.95, dotRed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gaugeColor(tc.score, threshold); got != tc.want {
				t.Errorf("gaugeColor(%v) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}
