package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/stopwatch"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/core"
)

const (
	commandPrompt    = "horizon › "
	bannerTopMargin  = 1
	clusterTagPrefix = "⎈ "
)

func (m *model) resize() {
	m.log.resize(m.logWidth(), minLogHeight)
	m.input.Width = m.inputWidth()
}

func (m model) logWidth() int {
	w := m.width
	if w < minLogWidth {
		return minLogWidth
	}
	return w
}

func (m model) inputWidth() int {
	w := m.width - len([]rune(m.input.Prompt)) - 1
	if w < 1 {
		w = 1
	}
	return w
}

func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	header := m.headerBand()
	inputBox := m.inputBand()

	if m.mode == modeHelp {
		body := m.helpOverlay(header, inputBox)
		out := lipgloss.JoinVertical(lipgloss.Left, header, body, inputBox)
		return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(out)
	}

	dashboard := m.dashboardBand()
	if !m.fits(header, dashboard, inputBox) {
		dashboard = m.collapsedDashboard()
	}

	logView := m.logBand(header, dashboard, inputBox)

	out := lipgloss.JoinVertical(lipgloss.Left, header, dashboard, logView, inputBox)
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(out)
}

func (m model) fits(header, dashboard, inputBox string) bool {
	used := lipgloss.Height(header) + lipgloss.Height(dashboard) + lipgloss.Height(inputBox)
	return m.height-used >= minLogHeight
}

func (m model) logBand(header, dashboard, inputBox string) string {
	used := lipgloss.Height(header) + lipgloss.Height(dashboard) + lipgloss.Height(inputBox)
	h := m.height - used
	if h < minLogHeight {
		h = minLogHeight
	}
	m.log.resize(m.logWidth(), h)
	return m.log.view.View()
}

func (m model) helpOverlay(header, inputBox string) string {
	h := m.height - lipgloss.Height(header) - lipgloss.Height(inputBox)
	if h < 1 {
		h = 1
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		helpTitleStyle.Render("commands"),
		renderHelp(),
		"",
		dimStyle.Render("press any key to dismiss"),
	)
	return lipgloss.NewStyle().Width(m.width).Height(h).MaxHeight(h).Render(content)
}

func (m model) headerBand() string {
	left := renderBanner(m.width, m.app.Cluster, m.context)
	if m.width < mediumBreakpoint {
		left = compactBanner(m.app.Cluster, m.context)
	}
	right := m.refreshIndicator()
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	return strings.Repeat("\n", bannerTopMargin) + row
}

func (m model) refreshIndicator() string {
	if m.loading {
		return m.spinner.View() + refreshLabelStyle.Render(" refreshing…")
	}
	if !m.loaded {
		return ""
	}
	return statusDot(dotGreen) + dimStyle.Render(fmt.Sprintf(" updated %s ago", ageLabel(m.age)))
}

func (m model) dashboardBand() string {
	if !m.loaded {
		return dimStyle.Render("loading cluster snapshot…")
	}
	pressure := renderPressure(m.snap.Pressure)
	gap := strings.Repeat("\n", sectionMargin)
	switch {
	case m.width >= wideBreakpoint:
		return lipgloss.JoinVertical(lipgloss.Left, pressure, gap, m.wideDashboard())
	case m.width >= mediumBreakpoint:
		return lipgloss.JoinVertical(lipgloss.Left, pressure, gap, m.mediumDashboard())
	default:
		return lipgloss.JoinVertical(lipgloss.Left, pressure, gap, m.narrowDashboard())
	}
}

func (m model) wideDashboard() string {
	colWidth := int(float64(m.width-columnGap) * twoColumnRatio)
	rightWidth := m.width - columnGap - colWidth
	left := nodesPanel(m.snap, colWidth, true)
	right := lipgloss.JoinVertical(lipgloss.Left,
		clusterStatusPanel(m.snap, rightWidth, false),
		clustersPanel(m.snap, rightWidth),
	)
	colHeight := max(lipgloss.Height(left), lipgloss.Height(right))
	left = lipgloss.NewStyle().Width(colWidth).Height(colHeight).Render(left)
	right = lipgloss.NewStyle().Width(rightWidth).Height(colHeight).Render(right)
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", columnGap), right)
	return lipgloss.JoinVertical(lipgloss.Left, top, poolsPanel(m.snap, m.width, true))
}

func (m model) mediumDashboard() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		nodesPanel(m.snap, m.width, false),
		poolsPanel(m.snap, m.width, true),
		clusterStatusPanel(m.snap, m.width, false),
		clustersPanel(m.snap, m.width),
	)
}

func (m model) narrowDashboard() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		nodesPanel(m.snap, m.width, false),
		poolsPanel(m.snap, m.width, false),
		clusterStatusPanel(m.snap, m.width, true),
	)
}

func (m model) collapsedDashboard() string {
	if !m.loaded {
		return dimStyle.Render("loading cluster snapshot…")
	}
	return dimStyle.Render(m.summaryLine())
}

func (m model) summaryLine() string {
	parts := []string{
		pressureSummaryLine(m.snap),
		fmt.Sprintf("%d nodes Ready", readyNodeCount(m.snap)),
		fmt.Sprintf("pools %d", len(m.snap.Pools)),
		controlPlaneGlyph(m.snap.Nudge),
	}
	return strings.Join(parts, " · ")
}

func (m model) inputBand() string {
	width := m.width
	topRule := ruleWithLabel(width, clusterTagPrefix+valueOr(m.app.Cluster, "default"))
	prompt := m.inputLine()
	bottomRule := inputRuleStyle.Render(strings.Repeat("─", width))
	strip := m.statusStrip(width)
	return lipgloss.JoinVertical(lipgloss.Left, topRule, prompt, bottomRule, strip)
}

func (m model) inputLine() string {
	switch m.mode {
	case modeCommand:
		return m.input.View()
	default:
		return inputRuleStyle.Render(m.input.Prompt)
	}
}

func ruleWithLabel(width int, label string) string {
	label = valueOr(label, "default")
	tag := " " + label + " "
	dashes := width - lipgloss.Width(tag) - 1
	if dashes < 1 {
		return inputRuleStyle.Render(strings.Repeat("─", width))
	}
	return inputRuleStyle.Render(strings.Repeat("─", dashes) + tag + "─")
}

func (m model) statusStrip(width int) string {
	switch m.mode {
	case modeCommand:
		return statusStripStyle.Render("enter run · esc cancel")
	case modeConfirm:
		return statusStripStyle.Render("y confirm · n/esc cancel")
	case modeRunning:
		return statusStripStyle.Render("running…")
	}
	left := strings.Join([]string{
		fmt.Sprintf("%s · ctx:%s", valueOr(m.app.Cluster, "default"), valueOr(m.context, "current")),
		fmt.Sprintf("%d nodes Ready", readyNodeCount(m.snap)),
		fmt.Sprintf("pools %d", len(m.snap.Pools)),
		controlPlaneGlyph(m.snap.Nudge),
		": command · ? help · q quit",
	}, " · ")
	right := ""
	if m.loaded {
		right = fmt.Sprintf("updated %s ago", ageLabel(m.age))
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return statusStripStyle.Render(left + strings.Repeat(" ", gap) + right)
}

func readyNodeCount(snap core.Snapshot) int {
	n := 0
	for _, r := range snap.Nodes {
		if r.Status == "Ready" {
			n++
		}
	}
	return n
}

func controlPlaneGlyph(state core.NudgeState) string {
	switch state.Kind {
	case core.NudgeInitialized:
		return "cp ✓"
	case core.NudgeUninitialized:
		return "cp !"
	case core.NudgeNotFound:
		return "cp -"
	default:
		return "cp ?"
	}
}

func ageLabel(sw stopwatch.Model) string {
	return fmt.Sprintf("%ds", int(sw.Elapsed().Seconds()))
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
