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

	gaugeWarnBand = 0.75
	gaugeCritBand = 0.90
)

func gaugeColor(score float64) lipgloss.AdaptiveColor {
	switch {
	case score >= gaugeCritBand:
		return theme.DotRed
	case score >= gaugeWarnBand:
		return theme.DotYellow
	default:
		return theme.DotGreen
	}
}

func statusDot(color lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(color).Render("●")
}

func adaptiveHex(c lipgloss.AdaptiveColor) string {
	if lipgloss.HasDarkBackground() {
		return c.Dark
	}
	return c.Light
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
	gauges := lipgloss.JoinHorizontal(
		lipgloss.Center,
		gauge("CPU", p.CPUScore),
		gaugeSpacing,
		gauge("Mem", p.MemScore),
		gaugeSpacing+dimStyle.Render(fmt.Sprintf("pending pods %d", p.PendingPods)),
	)
	parts = append(parts, gauges)
	return strings.Join(parts, "\n")
}

func gauge(label string, score float64) string {
	color := gaugeColor(score)
	bar := progress.New(
		progress.WithSolidFill(adaptiveHex(color)),
		progress.WithWidth(gaugeWidth),
		progress.WithoutPercentage(),
	)
	bar.EmptyColor = adaptiveHex(theme.GaugeBg)
	return fmt.Sprintf("%s %s %.2f %s", gaugeLabelStyle.Render(label), bar.ViewAs(score), score, statusDot(color))
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
	if full {
		fitNameColumn(headers, rows, 0, inner)
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
		return base.Foreground(theme.DotGreen)
	case "NotReady", "Unknown":
		return base.Foreground(theme.DotRed)
	}
	return base
}

func dimNeutralCell(base lipgloss.Style, value string) lipgloss.Style {
	if value == emptyCell || value == naCell {
		return base.Foreground(theme.Dim)
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
		for i, mc := range pool.Machines {
			name, typ, desired, ready := pool.Name, pool.Type, pool.Desired, pool.Ready
			if i > 0 {
				name, typ, desired, ready = "", "", "", ""
			}
			if mc.Err != nil {
				rows = append(rows, []string{name, typ, desired, ready, "error", mc.Err.Error(), emptyCell, emptyCell})
				continue
			}
			rows = append(rows, []string{
				name, typ, desired, ready,
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

func rightColumnNaturalWidth(snap core.Snapshot) int {
	w := lipgloss.Width("Cluster status")
	for _, line := range []string{nudgeLine(snap.Nudge), autoscalerLine(snap.Autoscaler)} {
		if x := lipgloss.Width(line); x > w {
			w = x
		}
	}
	if x := clustersNaturalWidth(snap); x > w {
		w = x
	}
	return w
}

func clustersNaturalWidth(snap core.Snapshot) int {
	if snap.ClustersErr != nil {
		return lipgloss.Width(fmt.Sprintf("unavailable: %v", snap.ClustersErr))
	}
	if len(snap.Clusters) == 0 {
		return lipgloss.Width("(no managed clusters)")
	}
	headers := []string{"NAME", "PHASE", "CP-INITIALIZED"}
	rows := make([][]string, 0, len(snap.Clusters))
	for _, c := range snap.Clusters {
		rows = append(rows, []string{c.Name, c.Phase, c.ControlPlaneReady})
	}
	return tableNaturalWidth(headers, rows)
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
		return fmt.Sprintf("control-plane %s %s", statusDot(theme.DotRed), errStyle.Render("status unavailable"))
	case core.NudgeUninitialized:
		return fmt.Sprintf("control-plane %s %s", statusDot(theme.DotYellow), warnStyle.Render("uninitialized (workers will not bootstrap)"))
	case core.NudgeInitialized:
		return fmt.Sprintf("control-plane %s initialized", statusDot(theme.DotGreen))
	default:
		return fmt.Sprintf("control-plane %s not found", statusDot(theme.DotYellow))
	}
}

func autoscalerLine(state core.AutoscalerState) string {
	switch {
	case state.NotFound:
		return fmt.Sprintf("autoscaler    %s not found", statusDot(theme.DotYellow))
	case state.Unavailable:
		return fmt.Sprintf("autoscaler    %s %s", statusDot(theme.DotYellow), warnStyle.Render("status unavailable"))
	default:
		return fmt.Sprintf("autoscaler    %s %s", statusDot(theme.DotGreen), state.Activity)
	}
}

func metricsPanel(snap core.Snapshot, width, height int) string {
	style := panelStyle
	inner := width - style.GetHorizontalFrameSize()
	if inner < 1 {
		inner = 1
	}
	title := lipgloss.NewStyle().MaxWidth(inner).Render(panelTitleStyle.Render("Metrics"))
	content := title + "\n" + metricsBody(snap)
	style = style.Width(inner)
	if h := height - style.GetVerticalFrameSize(); h > 0 {
		style = style.Height(h)
	}
	return style.Render(content)
}

func metricsBody(snap core.Snapshot) string {
	return strings.Join([]string{
		workloadSection(snap.Workload),
		nodeHealthSection(snap.NodeHealth),
		gitopsSection(snap.Flux),
	}, "\n\n")
}

func metricsContentHeight(snap core.Snapshot) int {
	return 1 + lipgloss.Height(metricsBody(snap))
}

func metricsContentWidth(snap core.Snapshot) int {
	w := lipgloss.Width(panelTitleStyle.Render("Metrics"))
	for _, line := range strings.Split(metricsBody(snap), "\n") {
		if x := lipgloss.Width(line); x > w {
			w = x
		}
	}
	return w
}

func workloadSection(w core.WorkloadSummary) string {
	if w.Err != nil {
		return subLabelStyle.Render("Workload") + "\n" + errStyle.Render(fmt.Sprintf("unavailable: %v", w.Err))
	}
	phases := fmt.Sprintf("%s %d Running   %s %d Pending   %s %d Failed",
		statusDot(theme.DotGreen), w.Running,
		statusDot(dotForCount(w.Pending, theme.DotYellow)), w.Pending,
		statusDot(dotForCount(w.Failed, theme.DotRed)), w.Failed)
	crash := fmt.Sprintf("%s %d CrashLoopBackOff", statusDot(dotForCount(w.CrashLoop, theme.DotRed)), w.CrashLoop)
	kinds := fmt.Sprintf("Deploy %d/%d   STS %d/%d   DS %d/%d",
		w.Deployments.Ready, w.Deployments.Desired,
		w.StatefulSets.Ready, w.StatefulSets.Desired,
		w.DaemonSets.Ready, w.DaemonSets.Desired)
	return strings.Join([]string{subLabelStyle.Render("Workload"), phases, crash, kinds}, "\n")
}

func nodeHealthSection(h core.NodeHealthSummary) string {
	if h.Err != nil {
		return subLabelStyle.Render("Node health") + "\n" + errStyle.Render(fmt.Sprintf("unavailable: %v", h.Err))
	}
	lines := []string{subLabelStyle.Render("Node health")}
	if len(h.Pressured) == 0 {
		lines = append(lines, fmt.Sprintf("%s no pressure", statusDot(theme.DotGreen)))
	} else {
		for _, p := range h.Pressured {
			lines = append(lines, fmt.Sprintf("%s %s %s", statusDot(theme.DotRed), p.Name, strings.Join(pressureFlags(p), " ")))
		}
	}
	lines = append(lines, fmt.Sprintf("committed  %s CPU %d%%   %s Mem %d%%",
		statusDot(gaugeColor(float64(h.CPUPercent())/100)), h.CPUPercent(),
		statusDot(gaugeColor(float64(h.MemPercent())/100)), h.MemPercent()))
	return strings.Join(lines, "\n")
}

func gitopsSection(f core.FluxSummary) string {
	return strings.Join([]string{
		subLabelStyle.Render("GitOps"),
		fluxLine("Kustomizations", f.Kustomizations, f.KustomizationsErr),
		fluxLine("HelmReleases", f.HelmReleases, f.HelmReleasesErr),
	}, "\n")
}

func fluxLine(label string, k core.FluxKind, err error) string {
	if err != nil {
		return fmt.Sprintf("%s %s %s", statusDot(theme.DotYellow), label, warnStyle.Render("unavailable"))
	}
	color := theme.DotGreen
	if k.Ready < k.Total {
		color = theme.DotRed
	}
	return fmt.Sprintf("%s %s %d/%d", statusDot(color), label, k.Ready, k.Total)
}

func dotForCount(n int, alert lipgloss.AdaptiveColor) lipgloss.AdaptiveColor {
	if n > 0 {
		return alert
	}
	return theme.DotGreen
}

func pressureFlags(p core.NodePressure) []string {
	var flags []string
	if p.Disk {
		flags = append(flags, "Disk")
	}
	if p.Memory {
		flags = append(flags, "Memory")
	}
	if p.PID {
		flags = append(flags, "PID")
	}
	return flags
}
