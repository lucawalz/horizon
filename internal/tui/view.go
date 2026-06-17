package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/core"
)

const (
	commandPrompt     = "horizon › "
	bannerTopMargin   = 1
	bannerPressureGap = 1
	clusterTagPrefix  = "⎈ "
)

func (m *model) relayout() {
	m.input.Width = m.inputWidth()
	if m.width <= 0 || m.height <= 0 {
		return
	}
	header := m.headerBand()
	inputBox := m.inputBand()
	dashboard := m.layoutDashboard(header, inputBox)
	m.log.resize(m.logWidth(), m.logHeight(header, dashboard, inputBox))
}

func (m model) layoutDashboard(header, inputBox string) string {
	dashboard := m.dashboardBand()
	if !m.fits(header, dashboard, inputBox) {
		return m.collapsedDashboard()
	}
	return dashboard
}

func (m model) logFloor() int {
	floor := minLogHeight
	if share := int(float64(m.height) * logHeightShare); share > floor {
		floor = share
	}
	return floor
}

func (m model) logHeight(header, dashboard, inputBox string) int {
	used := lipgloss.Height(header) + lipgloss.Height(dashboard) + lipgloss.Height(inputBox)
	h := m.height - used
	if floor := m.logFloor(); h < floor {
		h = floor
	}
	return h
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

	if m.mode == modeHelp {
		return m.helpOverlay()
	}

	if m.mode == modeThemePicker {
		return m.picker.view(m.width, m.height)
	}

	header := m.headerBand()
	inputBox := m.inputBand()
	dashboard := m.layoutDashboard(header, inputBox)

	out := lipgloss.JoinVertical(lipgloss.Left, header, dashboard, m.log.render(), inputBox)
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(out)
}

func (m model) fits(header, dashboard, inputBox string) bool {
	used := lipgloss.Height(header) + lipgloss.Height(dashboard) + lipgloss.Height(inputBox)
	return m.height-used >= m.logFloor()
}

func (m model) helpOverlay() string {
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		helpTitleStyle.Render("commands"),
		renderHelp(),
		"",
		dimStyle.Render("press any key to dismiss"),
	)
	box := modalStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
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
	return statusDot(theme.DotGreen)
}

func (m model) dashboardBand() string {
	if !m.loaded {
		return dimStyle.Render("loading cluster snapshot…")
	}
	gap := strings.Repeat("\n", bannerPressureGap)
	pressure := gap + renderPressure(m.snap.Pressure)
	switch {
	case m.width >= wideBreakpoint:
		return lipgloss.JoinVertical(lipgloss.Left, pressure, "", m.wideDashboard())
	case m.width >= mediumBreakpoint:
		return lipgloss.JoinVertical(lipgloss.Left, pressure, "", m.mediumDashboard())
	default:
		return lipgloss.JoinVertical(lipgloss.Left, pressure, "", m.narrowDashboard())
	}
}

func (m model) wideDashboard() string {
	colWidth, rightWidth := wideSplit(m.width-columnGap, rightColumnNaturalWidth(m.snap), panelStyle.GetHorizontalFrameSize())
	left := nodesPanel(m.snap, colWidth, true)
	right := lipgloss.JoinVertical(
		lipgloss.Left,
		clusterStatusPanel(m.snap, rightWidth, false),
		clustersPanel(m.snap, rightWidth),
	)
	colHeight := max(lipgloss.Height(left), lipgloss.Height(right))
	left = lipgloss.NewStyle().Width(colWidth).Height(colHeight).Render(left)
	right = lipgloss.NewStyle().Width(rightWidth).Height(colHeight).Render(right)
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", columnGap), right)
	return lipgloss.JoinVertical(lipgloss.Left, top, poolsPanel(m.snap, m.width, true))
}

func wideSplit(avail, rightContent, frame int) (left, right int) {
	right = rightContent + frame
	if hi := avail * 2 / 5; right > hi {
		right = hi
	}
	if right < minRightColWidth {
		right = minRightColWidth
	}
	return avail - right, right
}

func (m model) mediumDashboard() string {
	return lipgloss.JoinVertical(
		lipgloss.Left,
		nodesPanel(m.snap, m.width, false),
		poolsPanel(m.snap, m.width, true),
		clusterStatusPanel(m.snap, m.width, false),
		clustersPanel(m.snap, m.width),
	)
}

func (m model) narrowDashboard() string {
	return lipgloss.JoinVertical(
		lipgloss.Left,
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
	topRule := ruleWithLabel(width, m.log.scrollLabel(), clusterTagPrefix+valueOr(m.app.Cluster, "default"))
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

const (
	ruleLeadDashes  = 2
	ruleTrailDashes = 1
)

func ruleWithLabel(width int, leftLabel, rightLabel string) string {
	rightTag := " " + valueOr(rightLabel, "default") + " "
	leftTag := ""
	if leftLabel != "" {
		leftTag = " " + leftLabel + " "
	}
	fill := width - ruleLeadDashes - lipgloss.Width(leftTag) - lipgloss.Width(rightTag) - ruleTrailDashes
	if fill < 1 {
		return inputRuleStyle.Render(strings.Repeat("─", width))
	}
	rule := strings.Repeat("─", ruleLeadDashes) + leftTag + strings.Repeat("─", fill) + rightTag + strings.Repeat("─", ruleTrailDashes)
	return inputRuleStyle.Render(rule)
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
	return statusStripStyle.Render(lipgloss.NewStyle().Width(width).Render(left))
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

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
