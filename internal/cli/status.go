package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/lucawalz/horizon/internal/capi"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
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

	nodeUsage := fetchNodeUsage(ctx, app, w)

	printPressureHeader(ctx, app, w, nodeUsage)

	if err := printNodeTable(ctx, app, w, nodeUsage); err != nil {
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

type nodeUtilization struct {
	cpuPercent int
	memPercent int
	present    bool
}

func fetchNodeUsage(ctx context.Context, app *App, w io.Writer) map[string]*metricsv1beta1.NodeMetrics {
	list, err := app.MetricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(w, "warning: metrics-server unavailable: %v\n", err)
		return nil
	}
	usage := make(map[string]*metricsv1beta1.NodeMetrics, len(list.Items))
	for i := range list.Items {
		usage[list.Items[i].Name] = &list.Items[i]
	}
	return usage
}

func nodeUtilizationFor(node corev1.Node, usage map[string]*metricsv1beta1.NodeMetrics) nodeUtilization {
	metrics, ok := usage[node.Name]
	if !ok {
		return nodeUtilization{}
	}
	cpuUsed := metrics.Usage.Cpu().MilliValue()
	cpuAlloc := node.Status.Allocatable.Cpu().MilliValue()
	memUsed := metrics.Usage.Memory().Value()
	memAlloc := node.Status.Allocatable.Memory().Value()
	if cpuAlloc == 0 || memAlloc == 0 {
		return nodeUtilization{}
	}
	return nodeUtilization{
		cpuPercent: int(cpuUsed * 100 / cpuAlloc),
		memPercent: int(memUsed * 100 / memAlloc),
		present:    true,
	}
}

func printPressureHeader(ctx context.Context, app *App, w io.Writer, usage map[string]*metricsv1beta1.NodeMetrics) {
	nodes, err := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(w, "pressure: unavailable: %v\n", err)
		return
	}

	var cpuSum, memSum, ready int
	for _, node := range nodes.Items {
		if nodeStatus(node) != "Ready" {
			continue
		}
		util := nodeUtilizationFor(node, usage)
		if !util.present {
			continue
		}
		cpuSum += util.cpuPercent
		memSum += util.memPercent
		ready++
	}

	cpuScore := clusterScore(cpuSum, ready)
	memScore := clusterScore(memSum, ready)
	pendingCount := pendingPodCount(ctx, app, w)

	threshold := app.Config.Thresholds.Burst
	fmt.Fprintf(w, "CPU: %.2f/%.2f %s  Mem: %.2f/%.2f %s  Pending pods: %d\n",
		cpuScore, threshold, pressureDot(cpuScore, threshold),
		memScore, threshold, pressureDot(memScore, threshold),
		pendingCount,
	)
}

func clusterScore(sumPercent, count int) float64 {
	if count == 0 {
		return 0.0
	}
	return float64(sumPercent) / float64(count) / 100.0
}

func pendingPodCount(ctx context.Context, app *App, w io.Writer) int {
	pods, err := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Pending",
	})
	if err != nil {
		fmt.Fprintf(w, "warning: pending pods query failed: %v\n", err)
		return 0
	}
	return len(pods.Items)
}

func printNodeTable(ctx context.Context, app *App, out io.Writer, usage map[string]*metricsv1beta1.NodeMetrics) error {
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
		util := nodeUtilizationFor(node, usage)

		pods, _ := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + node.Name,
		})
		podCount := 0
		if pods != nil {
			podCount = len(pods.Items)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			node.Name, role, percentCell(util.cpuPercent, util.present), percentCell(util.memPercent, util.present), podCount, status, ip)
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

	fmt.Fprintln(w, "POOL\tTYPE\tDESIRED\tREADY\tMACHINE\tPHASE\tNODE\tPROVIDER-ID")

	for i := range pools {
		pool := pools[i]
		poolType := valueOrDash(capi.PoolType(&pool))
		desired := replicaCell(pool.Spec.Replicas)
		ready := replicaCell(pool.Status.ReadyReplicas)

		machines, err := app.CapiClient.ListMachines(ctx, app.Config.Pools.Namespace, pool.Name)
		if err != nil {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, poolType, desired, ready, "error", err.Error(), emptyCell, emptyCell)
			continue
		}
		if len(machines) == 0 {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, poolType, desired, ready, emptyCell, emptyCell, emptyCell, emptyCell)
			continue
		}
		for _, m := range machines {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pool.Name, poolType, desired, ready,
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

func printClusterTable(ctx context.Context, app *App, out io.Writer) {
	clusters, err := app.CapiClient.ListClusters(ctx, app.Config.Pools.Namespace)
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
	for i := range clusters {
		c := &clusters[i]
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			c.Name, valueOrDash(c.Status.Phase), boolOrDash(c.Status.Initialization.ControlPlaneInitialized))
	}
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
