package cli

import (
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/prometheus"
	"github.com/prometheus/common/model"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	cpuQuery     = `1 - avg by (instance)(rate(node_cpu_seconds_total{mode="idle"}[5m]))`
	memQuery     = `1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
	pendingQuery = `count(kube_pod_status_phase{phase="Pending"}==1) or vector(0)`
)

func newStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print cluster pressure and node table",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(app)
		},
	}
}

func runStatus(app *App) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pc, err := prometheus.NewClient(app.KubeClient, app.Config.Kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: prometheus unavailable: %v\n", err)
		return printNodeTable(ctx, app, nil, nil)
	}
	defer pc.Close()

	cpuVec, err := pc.QueryInstant(ctx, cpuQuery)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cpu query failed: %v\n", err)
	}
	memVec, err := pc.QueryInstant(ctx, memQuery)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: mem query failed: %v\n", err)
	}
	pendingVec, err := pc.QueryInstant(ctx, pendingQuery)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: pending query failed: %v\n", err)
	}

	cpuScore := avgVector(cpuVec)
	memScore := avgVector(memVec)
	pendingCount := 0
	if len(pendingVec) > 0 {
		pendingCount = int(pendingVec[0].Value)
	}

	threshold := app.Config.Thresholds.Burst

	fmt.Printf("CPU: %.2f/%.2f %s  Mem: %.2f/%.2f %s  Pending: %d\n",
		cpuScore, threshold, pressureDot(cpuScore, threshold),
		memScore, threshold, pressureDot(memScore, threshold),
		pendingCount,
	)
	printBurstPhase(ctx, app)
	fmt.Println()

	return printNodeTable(ctx, app, cpuVec, memVec)
}

func printNodeTable(ctx context.Context, app *App, cpuVec, memVec model.Vector) error {
	nodes, err := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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

func printBurstPhase(ctx context.Context, app *App) {
	fmt.Printf("BurstPhase: %s\n", k8s.ReadBurstPhase(ctx, app.KubeClient))
}

func PrintBurstPhaseForTest(ctx context.Context, app *App) {
	printBurstPhase(ctx, app)
}

func NodeMetricCellForTest(vec model.Vector, nodeIP string) string {
	return nodeMetricCell(vec, nodeIP)
}
