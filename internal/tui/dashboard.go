package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/core"
)

const naCell = "N/A"

func renderDashboard(snap core.Snapshot, width int) string {
	sections := []string{
		renderPressure(snap.Pressure),
		panel("Nodes", renderNodes(snap), width),
		panel("Pools", renderPools(snap), width),
		panel("Clusters", renderClusters(snap), width),
		renderNudge(snap.Nudge),
		renderAutoscaler(snap.Autoscaler),
	}
	return strings.Join(sections, "\n")
}

func panel(title, body string, width int) string {
	content := panelTitleStyle.Render(title) + "\n" + body
	style := panelStyle
	if width > 0 {
		inner := width - style.GetHorizontalFrameSize()
		if inner < 1 {
			inner = 1
		}
		style = style.Width(inner)
	}
	return style.Render(content)
}

func renderPressure(p core.PressureSummary) string {
	if p.Err != nil {
		return errStyle.Render(fmt.Sprintf("pressure: unavailable: %v", p.Err))
	}
	var parts []string
	if p.MetricsUnavailable != nil {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("warning: metrics-server unavailable: %v", p.MetricsUnavailable)))
	}
	if p.MetricsWarning != nil {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("warning: pending pods query failed: %v", p.MetricsWarning)))
	}
	parts = append(parts, fmt.Sprintf("CPU %.2f/%.2f %s  Mem %.2f/%.2f %s  Pending pods %d",
		p.CPUScore, p.Threshold, pressureDot(p.CPUScore, p.Threshold),
		p.MemScore, p.Threshold, pressureDot(p.MemScore, p.Threshold),
		p.PendingPods,
	))
	return strings.Join(parts, "\n")
}

func pressureDot(score, threshold float64) string {
	hex := dotGreen
	switch {
	case score >= threshold:
		hex = dotRed
	case score >= threshold*warnThresholdRatio:
		hex = dotYellow
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("●")
}

func renderNodes(snap core.Snapshot) string {
	if snap.NodesErr != nil {
		return errStyle.Render(fmt.Sprintf("unavailable: %v", snap.NodesErr))
	}
	rows := [][]string{{"NAME", "ROLE", "CPU%", "MEM%", "PODS", "STATUS", "IP"}}
	for _, r := range snap.Nodes {
		rows = append(rows, []string{
			r.Name, r.Role,
			percentCell(r.CPUPercent, r.MetricsPresent),
			percentCell(r.MemPercent, r.MetricsPresent),
			fmt.Sprintf("%d", r.PodCount),
			r.Status, r.IPv4,
		})
	}
	return renderTable(rows)
}

func percentCell(percent int, present bool) string {
	if !present {
		return naCell
	}
	return fmt.Sprintf("%d%%", percent)
}

func renderPools(snap core.Snapshot) string {
	if snap.PoolsErr != nil {
		return errStyle.Render(fmt.Sprintf("unavailable: %v", snap.PoolsErr))
	}
	if len(snap.Pools) == 0 {
		return dimStyle.Render("(no pools)")
	}
	rows := [][]string{{"POOL", "TYPE", "DESIRED", "READY", "MACHINE", "PHASE", "NODE", "PROVIDER-ID"}}
	for _, pool := range snap.Pools {
		if len(pool.Machines) == 0 {
			rows = append(rows, []string{pool.Name, pool.Type, pool.Desired, pool.Ready, emptyCell, emptyCell, emptyCell, emptyCell})
			continue
		}
		for _, m := range pool.Machines {
			if m.Err != nil {
				rows = append(rows, []string{pool.Name, pool.Type, pool.Desired, pool.Ready, "error", m.Err.Error(), emptyCell, emptyCell})
				continue
			}
			rows = append(rows, []string{
				pool.Name, pool.Type, pool.Desired, pool.Ready,
				m.Name, core.ValueOrDash(m.Phase), core.ValueOrDash(m.Node), core.ValueOrDash(m.ProviderID),
			})
		}
	}
	return renderTable(rows)
}

func renderClusters(snap core.Snapshot) string {
	if snap.ClustersErr != nil {
		return errStyle.Render(fmt.Sprintf("unavailable: %v", snap.ClustersErr))
	}
	if len(snap.Clusters) == 0 {
		return dimStyle.Render("(no managed clusters)")
	}
	rows := [][]string{{"NAME", "PHASE", "CP-INITIALIZED"}}
	for _, c := range snap.Clusters {
		rows = append(rows, []string{c.Name, c.Phase, c.ControlPlaneReady})
	}
	return renderTable(rows)
}

func renderNudge(state core.NudgeState) string {
	switch state.Kind {
	case core.NudgeError:
		return errStyle.Render(fmt.Sprintf("control-plane: status unavailable: %v", state.Err))
	case core.NudgeUninitialized:
		return warnStyle.Render("WARNING: externally-managed control plane not marked initialized; Mode-A workers will not bootstrap until nudged")
	case core.NudgeInitialized:
		return "control-plane: initialized"
	default:
		return ""
	}
}

func renderAutoscaler(state core.AutoscalerState) string {
	switch {
	case state.NotFound:
		return "autoscaler: not found"
	case state.Unavailable:
		return "autoscaler: status unavailable"
	default:
		return fmt.Sprintf("autoscaler: %s", state.Activity)
	}
}

const emptyCell = "-"
