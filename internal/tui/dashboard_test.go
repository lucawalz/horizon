package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func dotFor(hex string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("●")
}

func TestPressureDotTiers(t *testing.T) {
	const threshold = 0.8
	cases := []struct {
		name  string
		score float64
		want  string
	}{
		{"below warn ratio is green", 0.1, dotFor(dotGreen)},
		{"at warn ratio is yellow", threshold * warnThresholdRatio, dotFor(dotYellow)},
		{"between warn and threshold is yellow", 0.7, dotFor(dotYellow)},
		{"at threshold is red", threshold, dotFor(dotRed)},
		{"above threshold is red", 0.95, dotFor(dotRed)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pressureDot(tc.score, threshold); got != tc.want {
				t.Errorf("pressureDot(%v) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}
