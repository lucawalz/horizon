package tui

import "github.com/charmbracelet/lipgloss"

const (
	refreshInterval = 2

	gradientStartHex = "#22d3ee"
	gradientEndHex   = "#d946ef"

	panelHPadding = 1
	panelVPadding = 0

	modalHPadding = 2
	modalVPadding = 1

	wideBreakpoint   = 100
	mediumBreakpoint = 70

	gaugeWidth   = 24
	minLogHeight = 8
	minLogWidth  = 24
	columnGap    = 2

	logHeightShare = 0.35

	gaugeSpacing = "   "
)

type Theme struct {
	Accent  lipgloss.AdaptiveColor
	Dim     lipgloss.AdaptiveColor
	Title   lipgloss.AdaptiveColor
	Err     lipgloss.AdaptiveColor
	Warn    lipgloss.AdaptiveColor
	Border  lipgloss.AdaptiveColor
	GaugeBg lipgloss.AdaptiveColor

	DotGreen  lipgloss.AdaptiveColor
	DotYellow lipgloss.AdaptiveColor
	DotRed    lipgloss.AdaptiveColor
}

var theme = Theme{
	Accent:    lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#22d3ee"},
	Dim:       lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#6b7280"},
	Title:     lipgloss.AdaptiveColor{Light: "#111827", Dark: "#e5e7eb"},
	Err:       lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f87171"},
	Warn:      lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#eab308"},
	Border:    lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#6b7280"},
	GaugeBg:   lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#374151"},
	DotGreen:  lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#22c55e"},
	DotYellow: lipgloss.AdaptiveColor{Light: "#ca8a04", Dark: "#eab308"},
	DotRed:    lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#ef4444"},
}

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Border).
			Padding(panelVPadding, panelHPadding)

	panelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Accent)

	subLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Dim)

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(theme.Title)

	tableCellStyle = lipgloss.NewStyle().Padding(0, 1)

	dimStyle = lipgloss.NewStyle().Foreground(theme.Dim)

	errStyle = lipgloss.NewStyle().Foreground(theme.Err)

	warnStyle = lipgloss.NewStyle().Foreground(theme.Warn)

	statusStripStyle = lipgloss.NewStyle().Foreground(theme.Dim)

	inputRuleStyle = lipgloss.NewStyle().Foreground(theme.Dim)

	refreshLabelStyle = lipgloss.NewStyle().Foreground(theme.Accent)

	gaugeLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Accent)

	helpCommandStyle = lipgloss.NewStyle().Foreground(theme.Accent)

	helpTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Title)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Border).
			Padding(modalVPadding, modalHPadding)
)
