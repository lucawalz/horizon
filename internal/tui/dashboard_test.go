package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestGaugeColorTiers(t *testing.T) {
	cases := []struct {
		name  string
		score float64
		want  lipgloss.AdaptiveColor
	}{
		{"below warn band is green", 0.5, theme.DotGreen},
		{"just below warn band is green", gaugeWarnBand - 0.01, theme.DotGreen},
		{"at warn band is yellow", gaugeWarnBand, theme.DotYellow},
		{"between warn and crit is yellow", 0.8, theme.DotYellow},
		{"at crit band is red", gaugeCritBand, theme.DotRed},
		{"above crit band is red", 0.95, theme.DotRed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gaugeColor(tc.score); got != tc.want {
				t.Errorf("gaugeColor(%v) = %v, want %v", tc.score, got, tc.want)
			}
		})
	}
}
