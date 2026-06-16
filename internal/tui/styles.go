package tui

import "github.com/charmbracelet/lipgloss"

const (
	refreshInterval = 2

	gradientStartHex = "#22d3ee"
	gradientEndHex   = "#d946ef"

	panelHPadding = 1
	panelVPadding = 0

	dotRed    = "#ef4444"
	dotYellow = "#eab308"
	dotGreen  = "#22c55e"

	colorDim     = "#6b7280"
	colorTitle   = "#e5e7eb"
	colorErr     = "#f87171"
	colorWarn    = "#eab308"
	colorAccent  = "#22d3ee"
	colorGaugeBg = "#374151"

	warnThresholdRatio = 0.75

	wideBreakpoint   = 100
	mediumBreakpoint = 70

	gaugeWidth   = 24
	minLogHeight = 3
	minLogWidth  = 24
	columnGap    = 2

	gaugeSpacing  = "   "
	sectionMargin = 1

	twoColumnRatio = 0.5
)

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colorDim)).
			Padding(panelVPadding, panelHPadding)

	panelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorAccent))

	subLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorDim))

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color(colorTitle))

	tableCellStyle = lipgloss.NewStyle().Padding(0, 1)

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))

	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorErr))

	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarn))

	statusStripStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))

	inputRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))

	refreshLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent))

	gaugeLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTitle))

	helpCommandStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent))

	helpTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorTitle))
)
