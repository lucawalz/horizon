package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestGaugeColorTiers(t *testing.T) {
	const threshold = 0.8
	cases := []struct {
		name  string
		score float64
		want  lipgloss.AdaptiveColor
	}{
		{"below warn ratio is green", 0.1, theme.DotGreen},
		{"just above warn ratio is yellow", threshold*warnThresholdRatio + 0.01, theme.DotYellow},
		{"between warn and threshold is yellow", 0.7, theme.DotYellow},
		{"at threshold is red", threshold, theme.DotRed},
		{"above threshold is red", 0.95, theme.DotRed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gaugeColor(tc.score, threshold); got != tc.want {
				t.Errorf("gaugeColor(%v) = %v, want %v", tc.score, got, tc.want)
			}
		})
	}
}
