package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/lucawalz/horizon/internal/prometheus"
	"github.com/prometheus/common/model"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	cpuQuery     = `1 - avg by (instance)(rate(node_cpu_seconds_total{mode="idle"}[5m]))`
	memQuery     = `1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
	pendingQuery = `count(kube_pod_status_phase{phase="Pending"}==1) or vector(0)`

	autoscalerStatusNamespace = "kube-system"
	autoscalerStatusConfigMap = "cluster-autoscaler-status"
	autoscalerStatusKey       = "status"

	emptyCell = "-"
)

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

	cpuVec, memVec := printPressureHeader(ctx, app, w)

	if err := printNodeTable(ctx, app, w, cpuVec, memVec); err != nil {
		fmt.Fprintf(w, "nodes: unavailable: %v\n", err)
	}
	fmt.Fprintln(w)

	printPoolTable(ctx, app, w)
	fmt.Fprintln(w)

	printNudgeLine(ctx, app, w)
	printAutoscalerLine(ctx, app, w)

	return nil
}

func printPressureHeader(ctx context.Context, app *App, w io.Writer) (cpuVec, memVec model.Vector) {
	pc, err := prometheus.NewClient(app.KubeClient, app.Config.Kubeconfig)
	if err != nil {
		fmt.Fprintf(w, "pressure: unavailable: %v\n", err)
		return nil, nil
	}
	defer pc.Close()

	cpuVec, err = pc.QueryInstant(ctx, cpuQuery)
	if err != nil {
		fmt.Fprintf(w, "warning: cpu query failed: %v\n", err)
	}
	memVec, err = pc.QueryInstant(ctx, memQuery)
	if err != nil {
		fmt.Fprintf(w, "warning: mem query failed: %v\n", err)
	}
	pendingVec, err := pc.QueryInstant(ctx, pendingQuery)
	if err != nil {
		fmt.Fprintf(w, "warning: pending query failed: %v\n", err)
	}

	cpuScore := avgVector(cpuVec)
	memScore := avgVector(memVec)
	pendingCount := 0
	if len(pendingVec) > 0 {
		pendingCount = int(pendingVec[0].Value)
	}

	threshold := app.Config.Thresholds.Burst
	fmt.Fprintf(w, "CPU: %.2f/%.2f %s  Mem: %.2f/%.2f %s  Pending pods: %d\n",
		cpuScore, threshold, pressureDot(cpuScore, threshold),
		memScore, threshold, pressureDot(memScore, threshold),
		pendingCount,
	)
	return cpuVec, memVec
}

func printNodeTable(ctx context.Context, app *App, out io.Writer, cpuVec, memVec model.Vector) error {
	nodes, err := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer w.Flush()

	fmt.Fprintln(w, "NAME\tROLE\tCPU%\tMEM%\tPODS\tSTATUS\tIP")

	for _, node := range nodes.Items {
		role := nodeRole(node)
		status := nodeStatus(node)
		ip := getNodeIPv4(node)

		pods, _ := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + node.Name,
		})
		podCount := 0
		if pods != nil {
			podCount = len(pods.Items)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			node.Name, role, nodeMetricCell(cpuVec, ip), nodeMetricCell(memVec, ip), podCount, status, ip)
	}
	return nil
}

func printPoolTable(ctx context.Context, app *App, out io.Writer) {
	pools, err := listPoolsForStatus(ctx, app)
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

	fmt.Fprintln(w, "POOL\tDESIRED\tREADY\tMACHINE\tPHASE\tNODE\tPROVIDER-ID")

	for _, pool := range pools {
		desired := replicaCell(pool.Spec.Replicas)
		ready := replicaCell(pool.Status.ReadyReplicas)

		machines, err := app.CapiClient.ListMachines(ctx, app.Config.Pools.Namespace, pool.Name)
		if err != nil {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, desired, ready, "error", err.Error(), emptyCell, emptyCell)
			continue
		}
		if len(machines) == 0 {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, desired, ready, emptyCell, emptyCell, emptyCell, emptyCell)
			continue
		}
		for _, m := range machines {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, desired, ready,
				m.Name, valueOrDash(m.Status.Phase),
				valueOrDash(m.Status.NodeRef.Name), valueOrDash(m.Spec.ProviderID))
		}
	}
}

func listPoolsForStatus(ctx context.Context, app *App) ([]clusterv1.MachineDeployment, error) {
	if app.Cluster == "" {
		return app.CapiClient.ListPools(ctx, app.Config.Pools.Namespace)
	}
	return app.CapiClient.ListPoolsForCluster(ctx, app.Config.Pools.Namespace, app.Cluster)
}

func printNudgeLine(ctx context.Context, app *App, w io.Writer) {
	initialized, err := app.CapiClient.IsControlPlaneInitialized(ctx, app.Config.Pools.Namespace, app.Cluster)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(w, "control-plane: status unavailable: %v\n", err)
		return
	}
	if !initialized {
		fmt.Fprintf(w, "%s externally-managed control plane not marked initialized; Mode-A workers will not bootstrap until nudged\n",
			color.YellowString("WARNING:"))
		return
	}
	fmt.Fprintln(w, "control-plane: initialized")
}

func printAutoscalerLine(ctx context.Context, app *App, w io.Writer) {
	cm, err := app.KubeClient.CoreV1().ConfigMaps(autoscalerStatusNamespace).Get(ctx, autoscalerStatusConfigMap, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		fmt.Fprintln(w, "autoscaler: not found")
		return
	}
	if err != nil {
		fmt.Fprintln(w, "autoscaler: status unavailable")
		return
	}
	fmt.Fprintf(w, "autoscaler: %s\n", autoscalerActivity(cm.Data))
}

func autoscalerActivity(data map[string]string) string {
	raw, ok := data[autoscalerStatusKey]
	if !ok || strings.TrimSpace(raw) == "" {
		return "status unavailable"
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Cluster-wide:") || strings.HasPrefix(trimmed, "Health:") {
			return trimmed
		}
	}
	return strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
}

func replicaCell(n *int32) string {
	if n == nil {
		return "0"
	}
	return fmt.Sprintf("%d", *n)
}

func valueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return emptyCell
	}
	return s
}

func nodeMetricCell(vec model.Vector, nodeIP string) string {
	if nodeIP == "" || nodeIP == "N/A" {
		return "N/A"
	}
	for _, s := range vec {
		host, _, err := net.SplitHostPort(string(s.Metric["instance"]))
		if err != nil {
			host = string(s.Metric["instance"])
		}
		if host != nodeIP {
			continue
		}
		v := float64(s.Value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "N/A"
		}
		return fmt.Sprintf("%d%%", int(math.Round(v*100)))
	}
	return "N/A"
}

func getNodeIPv4(node corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP && !strings.Contains(addr.Address, ":") {
			return addr.Address
		}
	}
	return "N/A"
}

func nodeRole(node corev1.Node) string {
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return "master"
	}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return "master"
	}
	return "worker"
}

func nodeStatus(node corev1.Node) string {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			if cond.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func avgVector(vec model.Vector) float64 {
	if len(vec) == 0 {
		return 0.0
	}
	var sum float64
	for _, s := range vec {
		sum += float64(s.Value)
	}
	return sum / float64(len(vec))
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

func NodeMetricCellForTest(vec model.Vector, nodeIP string) string {
	return nodeMetricCell(vec, nodeIP)
}
