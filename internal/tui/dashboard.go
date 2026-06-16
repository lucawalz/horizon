package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/lucawalz/horizon/internal/core"
)

const (
	naCell    = "N/A"
	emptyCell = "-"
)

func gaugeColor(score, threshold float64) string {
	switch {
	case score >= threshold:
		return dotRed
	case score >= threshold*warnThresholdRatio:
		return dotYellow
	default:
		return dotGreen
	}
}

func statusDot(hex string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("●")
}

func renderPressure(p core.PressureSummary) string {
	if p.Err != nil {
		return errStyle.Render(fmt.Sprintf("pressure: unavailable: %v", p.Err))
	}
	var parts []string
	if p.MetricsUnavailable != nil {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("metrics unavailable: %v", p.MetricsUnavailable)))
	}
	if p.MetricsWarning != nil {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("pending pods query failed: %v", p.MetricsWarning)))
	}
	if p.MetricsUnavailable != nil {
		parts = append(parts, fmt.Sprintf("pending pods %d", p.PendingPods))
		return strings.Join(parts, "\n")
	}
	gauges := lipgloss.JoinHorizontal(lipgloss.Center,
		gauge("CPU", p.CPUScore, p.Threshold),
		gaugeSpacing,
		gauge("Mem", p.MemScore, p.Threshold),
		gaugeSpacing+dimStyle.Render(fmt.Sprintf("pending pods %d", p.PendingPods)),
	)
	parts = append(parts, gauges)
	return strings.Join(parts, "\n")
}

func gauge(label string, score, threshold float64) string {
	hex := gaugeColor(score, threshold)
	bar := progress.New(
		progress.WithSolidFill(hex),
		progress.WithWidth(gaugeWidth),
		progress.WithoutPercentage(),
	)
	bar.EmptyColor = colorGaugeBg
	return fmt.Sprintf("%s %s %.2f/%.2f %s", gaugeLabelStyle.Render(label), bar.ViewAs(score), score, threshold, statusDot(hex))
}

func pressureSummaryLine(snap core.Snapshot) string {
	p := snap.Pressure
	if p.Err != nil {
		return "pressure unavailable"
	}
	if p.MetricsUnavailable != nil {
		return fmt.Sprintf("metrics unavailable · pending pods %d", p.PendingPods)
	}
	return fmt.Sprintf("CPU %.2f Mem %.2f", p.CPUScore, p.MemScore)
}

func titledPanel(title string, width int, body func(inner int) string) string {
	style := panelStyle
	inner := width - style.GetHorizontalFrameSize()
	if inner < 1 {
		inner = 1
	}
	title = lipgloss.NewStyle().MaxWidth(inner).Render(panelTitleStyle.Render(title))
	content := title + "\n" + body(inner)
	return style.Width(inner).Render(content)
}

func nodesPanel(snap core.Snapshot, width int, full bool) string {
	return titledPanel("Nodes", width, func(inner int) string { return nodesBody(snap, inner, full) })
}

func nodesBody(snap core.Snapshot, inner int, full bool) string {
	if snap.NodesErr != nil {
		return errStyle.Render(fmt.Sprintf("unavailable: %v", snap.NodesErr))
	}
	headers := []string{"NAME", "ROLE", "CPU%", "MEM%", "PODS", "STATUS", "IP"}
	if !full {
		headers = []string{"NAME", "CPU%", "MEM%", "STATUS"}
	}
	rows := make([][]string, 0, len(snap.Nodes))
	for _, r := range snap.Nodes {
		cpu := percentCell(r.CPUPercent, r.MetricsPresent)
		mem := percentCell(r.MemPercent, r.MetricsPresent)
		if full {
			rows = append(rows, []string{r.Name, r.Role, cpu, mem, fmt.Sprintf("%d", r.PodCount), r.Status, r.IPv4})
		} else {
			rows = append(rows, []string{r.Name, cpu, mem, r.Status})
		}
	}
	statusCol := len(headers) - 1
	t := newPanelTable(headers, inner, func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return tableHeaderStyle.Padding(0, 1).PaddingLeft(0)
		}
		base := tableCellStyle.PaddingLeft(0)
		if col == statusCol {
			return statusCellStyle(base, rows[row][col])
		}
		return dimNeutralCell(base, rows[row][col])
	}).Rows(rows...)
	return t.Render()
}

func statusCellStyle(base lipgloss.Style, value string) lipgloss.Style {
	switch value {
	case "Ready":
		return base.Foreground(lipgloss.Color(dotGreen))
	case "NotReady", "Unknown":
		return base.Foreground(lipgloss.Color(dotRed))
	}
	return base
}

func dimNeutralCell(base lipgloss.Style, value string) lipgloss.Style {
	if value == emptyCell || value == naCell {
		return base.Foreground(lipgloss.Color(colorDim))
	}
	return base
}

func percentCell(percent int, present bool) string {
	if !present {
		return naCell
	}
	return fmt.Sprintf("%d%%", percent)
}

func poolsPanel(snap core.Snapshot, width int, full bool) string {
	return titledPanel("Pools", width, func(inner int) string { return poolsBody(snap, inner, full) })
}

func poolsBody(snap core.Snapshot, inner int, full bool) string {
	if snap.PoolsErr != nil {
		return errStyle.Render(fmt.Sprintf("unavailable: %v", snap.PoolsErr))
	}
	if len(snap.Pools) == 0 {
		return dimStyle.Render("(no pools)")
	}
	if !full {
		return poolsCompactBody(snap, inner)
	}
	headers := []string{"POOL", "TYPE", "DESIRED", "READY", "MACHINE", "PHASE", "NODE", "PROVIDER-ID"}
	rows := make([][]string, 0)
	for _, pool := range snap.Pools {
		if len(pool.Machines) == 0 {
			rows = append(rows, []string{pool.Name, pool.Type, pool.Desired, pool.Ready, emptyCell, emptyCell, emptyCell, emptyCell})
			continue
		}
		for _, mc := range pool.Machines {
			if mc.Err != nil {
				rows = append(rows, []string{pool.Name, pool.Type, pool.Desired, pool.Ready, "error", mc.Err.Error(), emptyCell, emptyCell})
				continue
			}
			rows = append(rows, []string{
				pool.Name, pool.Type, pool.Desired, pool.Ready,
				mc.Name, core.ValueOrDash(mc.Phase), core.ValueOrDash(mc.Node), core.ValueOrDash(mc.ProviderID),
			})
		}
	}
	return neutralTable(headers, rows, inner)
}

func poolsCompactBody(snap core.Snapshot, inner int) string {
	headers := []string{"POOL", "TYPE", "DESIRED/READY"}
	rows := make([][]string, 0, len(snap.Pools))
	for _, pool := range snap.Pools {
		rows = append(rows, []string{pool.Name, pool.Type, pool.Desired + "/" + pool.Ready})
	}
	return neutralTable(headers, rows, inner)
}

func clustersPanel(snap core.Snapshot, width int) string {
	return titledPanel("Clusters", width, func(inner int) string { return clustersBody(snap, inner) })
}

func clustersBody(snap core.Snapshot, inner int) string {
	if snap.ClustersErr != nil {
		return errStyle.Render(fmt.Sprintf("unavailable: %v", snap.ClustersErr))
	}
	if len(snap.Clusters) == 0 {
		return dimStyle.Render("(no managed clusters)")
	}
	headers := []string{"NAME", "PHASE", "CP-INITIALIZED"}
	rows := make([][]string, 0, len(snap.Clusters))
	for _, c := range snap.Clusters {
		rows = append(rows, []string{c.Name, c.Phase, c.ControlPlaneReady})
	}
	return neutralTable(headers, rows, inner)
}

func clusterStatusPanel(snap core.Snapshot, width int, foldClusters bool) string {
	return titledPanel("Cluster status", width, func(inner int) string {
		body := strings.Join([]string{nudgeLine(snap.Nudge), autoscalerLine(snap.Autoscaler)}, "\n")
		if foldClusters {
			body += "\n" + subLabelStyle.Render("clusters") + "\n" + clustersBody(snap, inner)
		}
		return body
	})
}

func neutralTable(headers []string, rows [][]string, inner int) string {
	t := newPanelTable(headers, inner, func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return tableHeaderStyle.Padding(0, 1).PaddingLeft(0)
		}
		return dimNeutralCell(tableCellStyle.PaddingLeft(0), rows[row][col])
	}).Rows(rows...)
	return t.Render()
}

func nudgeLine(state core.NudgeState) string {
	switch state.Kind {
	case core.NudgeError:
		return fmt.Sprintf("control-plane %s %s", statusDot(dotRed), errStyle.Render("status unavailable"))
	case core.NudgeUninitialized:
		return fmt.Sprintf("control-plane %s %s", statusDot(dotYellow), warnStyle.Render("uninitialized (workers will not bootstrap until nudged)"))
	case core.NudgeInitialized:
		return fmt.Sprintf("control-plane %s initialized", statusDot(dotGreen))
	default:
		return fmt.Sprintf("control-plane %s not found", statusDot(dotYellow))
	}
}

func autoscalerLine(state core.AutoscalerState) string {
	switch {
	case state.NotFound:
		return fmt.Sprintf("autoscaler    %s not found", statusDot(dotYellow))
	case state.Unavailable:
		return fmt.Sprintf("autoscaler    %s %s", statusDot(dotYellow), warnStyle.Render("status unavailable"))
	default:
		return fmt.Sprintf("autoscaler    %s %s", statusDot(dotGreen), state.Activity)
	}
}
