package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	commandPrompt = "horizon › "

	bannerTopMargin = 1
	columnGapWidth  = 2
	minDashboardCol = 40
	minLogCol       = 24
	dashboardRatio  = 0.55
	chromeRows      = 4
)

func (m *model) resize() {
	_, logWidth := splitWidths(m.width)
	m.log.resize(logWidth, m.bodyHeight())
	m.input.Width = m.inputWidth()
}

func splitWidths(total int) (dashboard, log int) {
	if total <= 0 {
		return minDashboardCol, minLogCol
	}
	usable := total - columnGapWidth
	if usable < minDashboardCol+minLogCol {
		return minDashboardCol, minLogCol
	}
	dashboard = int(float64(usable) * dashboardRatio)
	if dashboard < minDashboardCol {
		dashboard = minDashboardCol
	}
	log = usable - dashboard
	if log < minLogCol {
		log = minLogCol
		dashboard = usable - log
	}
	return dashboard, log
}

func (m model) bodyHeight() int {
	bannerRows := bannerTopMargin + len(strings.Split(bannerArt, "\n")) + 1
	h := m.height - bannerRows - chromeRows
	if h < 1 {
		h = 1
	}
	return h
}

func (m model) inputWidth() int {
	w := m.width - len(commandPrompt) - 1
	if w < 1 {
		w = 1
	}
	return w
}

func (m model) View() string {
	dashWidth, _ := splitWidths(m.width)

	var b strings.Builder
	b.WriteString(strings.Repeat("\n", bannerTopMargin))
	b.WriteString(renderBanner(m.width, m.app.Cluster, m.context))
	b.WriteString("\n\n")
	b.WriteString(m.body(dashWidth))
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(footerStyle.Render(m.footer()))
	return b.String()
}

func (m model) body(dashWidth int) string {
	left := m.dashboardView(dashWidth)
	right := m.log.view.View()
	gap := strings.Repeat(" ", columnGapWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
}

func (m model) dashboardView(width int) string {
	if !m.loaded {
		return dimStyle.Render("loading cluster snapshot…")
	}
	return renderDashboard(m.snap, width)
}

func (m model) footer() string {
	switch m.mode {
	case modeCommand:
		return "enter run  esc cancel"
	case modeConfirm:
		return "y confirm  n/esc cancel"
	case modeRunning:
		return "running…"
	default:
		if m.showHelp {
			return strings.Join(helpLines(), "\n")
		}
		return ": command  ↑↓ scroll  pgup/pgdn page  r refresh  ? help  q quit"
	}
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
