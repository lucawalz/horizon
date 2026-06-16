package tui

import "github.com/charmbracelet/lipgloss"

const (
	refreshInterval = 10

	gradientStartHex = "#22d3ee"
	gradientEndHex   = "#d946ef"

	panelHPadding = 1
	panelVPadding = 0

	dotRed    = "#ef4444"
	dotYellow = "#eab308"
	dotGreen  = "#22c55e"

	colorDim   = "#6b7280"
	colorTitle = "#e5e7eb"
	colorErr   = "#f87171"
	colorWarn  = "#eab308"

	warnThresholdRatio = 0.75
)

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colorDim)).
			Padding(panelVPadding, panelHPadding)

	panelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorTitle))

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color(colorTitle))

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))

	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorErr))

	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarn))

	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))
)
