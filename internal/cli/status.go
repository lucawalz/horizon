package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/spf13/cobra"
)

const emptyCell = "-"

func newStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print read-only cluster pressure, pools, and autoscaler status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), app, os.Stdout)
		},
	}
}

func runStatus(ctx context.Context, app *App, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	usage, err := core.FetchNodeUsage(ctx, app)
	if err != nil {
		fmt.Fprintf(w, "warning: metrics-server unavailable: %v\n", err)
	}

	printPressureHeader(ctx, app, w, usage)

	if err := printNodeTable(ctx, app, w, usage); err != nil {
		fmt.Fprintf(w, "nodes: unavailable: %v\n", err)
	}
	fmt.Fprintln(w)

	printPoolTable(ctx, app, w)
	fmt.Fprintln(w)

	printClusterTable(ctx, app, w)
	fmt.Fprintln(w)

	printNudgeLine(ctx, app, w)
	printAutoscalerLine(ctx, app, w)

	return nil
}

func printPressureHeader(ctx context.Context, app *App, w io.Writer, usage core.NodeUsage) {
	p := core.PressureFor(ctx, app, usage)
	if p.Err != nil {
		fmt.Fprintf(w, "pressure: unavailable: %v\n", p.Err)
		return
	}
	if p.MetricsWarning != nil {
		fmt.Fprintf(w, "warning: pending pods query failed: %v\n", p.MetricsWarning)
	}
	fmt.Fprintf(w, "CPU: %.2f/%.2f %s  Mem: %.2f/%.2f %s  Pending pods: %d\n",
		p.CPUScore, p.Threshold, pressureDot(p.CPUScore, p.Threshold),
		p.MemScore, p.Threshold, pressureDot(p.MemScore, p.Threshold),
		p.PendingPods,
	)
}

func printNodeTable(ctx context.Context, app *App, out io.Writer, usage core.NodeUsage) error {
	rows, err := core.NodeRows(ctx, app, usage)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer w.Flush()

	fmt.Fprintln(w, "NAME\tROLE\tCPU%\tMEM%\tPODS\tSTATUS\tIP")

	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			r.Name, r.Role, percentCell(r.CPUPercent, r.MetricsPresent), percentCell(r.MemPercent, r.MetricsPresent), r.PodCount, r.Status, r.IPv4)
	}
	return nil
}

func percentCell(percent int, present bool) string {
	if !present {
		return "N/A"
	}
	return fmt.Sprintf("%d%%", percent)
}

func printPoolTable(ctx context.Context, app *App, out io.Writer) {
	pools, err := core.PoolRows(ctx, app)
	if err != nil {
		fmt.Fprintf(out, "pools: unavailable: %v\n", err)
		return
	}
	if len(pools) == 0 {
		fmt.Fprintln(out, "pools: none")
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer w.Flush()

	fmt.Fprintln(w, "POOL\tTYPE\tDESIRED\tREADY\tMACHINE\tPHASE\tNODE\tPROVIDER-ID")

	for _, pool := range pools {
		if len(pool.Machines) == 0 {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, pool.Type, pool.Desired, pool.Ready, emptyCell, emptyCell, emptyCell, emptyCell)
			continue
		}
		for _, m := range pool.Machines {
			if m.Err != nil {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					pool.Name, pool.Type, pool.Desired, pool.Ready, "error", m.Err.Error(), emptyCell, emptyCell)
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, pool.Type, pool.Desired, pool.Ready,
				m.Name, core.ValueOrDash(m.Phase),
				core.ValueOrDash(m.Node), core.ValueOrDash(m.ProviderID))
		}
	}
}

func printClusterTable(ctx context.Context, app *App, out io.Writer) {
	clusters, err := core.ClusterRows(ctx, app)
	if err != nil {
		fmt.Fprintf(out, "clusters: unavailable: %v\n", err)
		return
	}
	fmt.Fprintln(out, "Clusters")
	if len(clusters) == 0 {
		fmt.Fprintln(out, "(no managed clusters)")
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "NAME\tPHASE\tCP-INITIALIZED")
	for _, c := range clusters {
		fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.Phase, c.ControlPlaneReady)
	}
}

func printNudgeLine(ctx context.Context, app *App, w io.Writer) {
	state := core.NudgeStateFor(ctx, app)
	switch state.Kind {
	case core.NudgeNotFound:
		return
	case core.NudgeError:
		fmt.Fprintf(w, "control-plane: status unavailable: %v\n", state.Err)
	case core.NudgeUninitialized:
		fmt.Fprintf(w, "%s externally-managed control plane not marked initialized; Mode-A workers will not bootstrap until nudged\n",
			color.YellowString("WARNING:"))
	case core.NudgeInitialized:
		fmt.Fprintln(w, "control-plane: initialized")
	}
}

func printAutoscalerLine(ctx context.Context, app *App, w io.Writer) {
	state := core.AutoscalerStateFor(ctx, app)
	switch {
	case state.NotFound:
		fmt.Fprintln(w, "autoscaler: not found")
	case state.Unavailable:
		fmt.Fprintln(w, "autoscaler: status unavailable")
	default:
		fmt.Fprintf(w, "autoscaler: %s\n", state.Activity)
	}
}

func pressureDot(score, threshold float64) string {
	if score >= threshold {
		return color.RedString("●")
	}
	if score >= threshold*0.75 {
		return color.YellowString("●")
	}
	return color.GreenString("●")
}

func RunStatusForTest(ctx context.Context, app *App, w io.Writer) error {
	return runStatus(ctx, app, w)
}
