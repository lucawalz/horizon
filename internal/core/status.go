package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucawalz/horizon/internal/capi"
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

type NodeUsage = map[string]*metricsv1beta1.NodeMetrics

type NudgeKind int

const (
	NudgeNotFound NudgeKind = iota
	NudgeInitialized
	NudgeUninitialized
	NudgeError
)

type NodeUtilization struct {
	CPUPercent int
	MemPercent int
	Present    bool
}

type PressureSummary struct {
	Available          bool
	Err                error
	CPUScore           float64
	MemScore           float64
	Threshold          float64
	PendingPods        int
	MetricsWarning     error
	MetricsUnavailable error
}

type NodeRow struct {
	Name           string
	Role           string
	CPUPercent     int
	MemPercent     int
	MetricsPresent bool
	PodCount       int
	Status         string
	IPv4           string
}

type MachineRow struct {
	Name       string
	Phase      string
	Node       string
	ProviderID string
	Err        error
}

type PoolRow struct {
	Name     string
	Type     string
	Desired  string
	Ready    string
	Machines []MachineRow
}

type ClusterRow struct {
	Name              string
	Phase             string
	ControlPlaneReady string
}

type NudgeState struct {
	Kind NudgeKind
	Err  error
}

type AutoscalerState struct {
	NotFound    bool
	Unavailable bool
	Activity    string
}

type Snapshot struct {
	Pressure    PressureSummary
	NodesErr    error
	Nodes       []NodeRow
	PoolsErr    error
	Pools       []PoolRow
	ClustersErr error
	Clusters    []ClusterRow
	Nudge       NudgeState
	Autoscaler  AutoscalerState
}

func BuildSnapshot(ctx context.Context, app *App) Snapshot {
	usage, usageErr := FetchNodeUsage(ctx, app)
	snap := Snapshot{
		Pressure:   PressureFor(ctx, app, usage),
		Nudge:      NudgeStateFor(ctx, app),
		Autoscaler: AutoscalerStateFor(ctx, app),
	}
	if usageErr != nil {
		snap.Pressure.MetricsUnavailable = usageErr
	}
	snap.Nodes, snap.NodesErr = NodeRows(ctx, app, usage)
	snap.Pools, snap.PoolsErr = PoolRows(ctx, app)
	snap.Clusters, snap.ClustersErr = ClusterRows(ctx, app)
	return snap
}

func FetchNodeUsage(ctx context.Context, app *App) (map[string]*metricsv1beta1.NodeMetrics, error) {
	list, err := app.MetricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	usage := make(map[string]*metricsv1beta1.NodeMetrics, len(list.Items))
	for i := range list.Items {
		usage[list.Items[i].Name] = &list.Items[i]
	}
	return usage, nil
}

func PressureFor(ctx context.Context, app *App, usage map[string]*metricsv1beta1.NodeMetrics) PressureSummary {
	nodes, err := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return PressureSummary{Err: err}
	}

	var cpuSum, memSum, ready int
	for _, node := range nodes.Items {
		if NodeStatus(node) != "Ready" {
			continue
		}
		util := NodeUtilizationFor(node, usage)
		if !util.Present {
			continue
		}
		cpuSum += util.CPUPercent
		memSum += util.MemPercent
		ready++
	}

	pending, perr := pendingPodCount(ctx, app)
	return PressureSummary{
		Available:      true,
		CPUScore:       clusterScore(cpuSum, ready),
		MemScore:       clusterScore(memSum, ready),
		Threshold:      app.Config.Thresholds.Burst,
		PendingPods:    pending,
		MetricsWarning: perr,
	}
}

func NodeRows(ctx context.Context, app *App, usage map[string]*metricsv1beta1.NodeMetrics) ([]NodeRow, error) {
	nodes, err := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	rows := make([]NodeRow, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		util := NodeUtilizationFor(node, usage)
		pods, _ := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + node.Name,
		})
		podCount := 0
		if pods != nil {
			podCount = len(pods.Items)
		}
		rows = append(rows, NodeRow{
			Name:           node.Name,
			Role:           NodeRole(node),
			CPUPercent:     util.CPUPercent,
			MemPercent:     util.MemPercent,
			MetricsPresent: util.Present,
			PodCount:       podCount,
			Status:         NodeStatus(node),
			IPv4:           GetNodeIPv4(node),
		})
	}
	return rows, nil
}

func PoolRows(ctx context.Context, app *App) ([]PoolRow, error) {
	pools, err := listPoolsForStatus(ctx, app)
	if err != nil {
		return nil, err
	}
	rows := make([]PoolRow, 0, len(pools))
	for i := range pools {
		pool := pools[i]
		row := PoolRow{
			Name:    pool.Name,
			Type:    ValueOrDash(capi.PoolType(&pool)),
			Desired: ReplicaCell(pool.Spec.Replicas),
			Ready:   ReplicaCell(pool.Status.ReadyReplicas),
		}
		machines, err := app.CapiClient.ListMachines(ctx, app.Config.Pools.Namespace, pool.Name)
		if err != nil {
			row.Machines = []MachineRow{{Err: err}}
			rows = append(rows, row)
			continue
		}
		for _, m := range machines {
			row.Machines = append(row.Machines, MachineRow{
				Name:       m.Name,
				Phase:      m.Status.Phase,
				Node:       m.Status.NodeRef.Name,
				ProviderID: m.Spec.ProviderID,
			})
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func listPoolsForStatus(ctx context.Context, app *App) ([]clusterv1.MachineDeployment, error) {
	if app.Cluster == "" {
		return app.CapiClient.ListPools(ctx, app.Config.Pools.Namespace)
	}
	return app.CapiClient.ListPoolsForCluster(ctx, app.Config.Pools.Namespace, app.Cluster)
}

func ClusterRows(ctx context.Context, app *App) ([]ClusterRow, error) {
	clusters, err := app.CapiClient.ListClusters(ctx, app.Config.Pools.Namespace)
	if err != nil {
		return nil, err
	}
	rows := make([]ClusterRow, 0, len(clusters))
	for i := range clusters {
		c := &clusters[i]
		rows = append(rows, ClusterRow{
			Name:              c.Name,
			Phase:             ValueOrDash(c.Status.Phase),
			ControlPlaneReady: BoolOrDash(c.Status.Initialization.ControlPlaneInitialized),
		})
	}
	return rows, nil
}

func NudgeStateFor(ctx context.Context, app *App) NudgeState {
	initialized, err := app.CapiClient.IsControlPlaneInitialized(ctx, app.Config.Pools.Namespace, app.Cluster)
	if apierrors.IsNotFound(err) {
		return NudgeState{Kind: NudgeNotFound}
	}
	if err != nil {
		return NudgeState{Kind: NudgeError, Err: err}
	}
	if !initialized {
		return NudgeState{Kind: NudgeUninitialized}
	}
	return NudgeState{Kind: NudgeInitialized}
}

func AutoscalerStateFor(ctx context.Context, app *App) AutoscalerState {
	cm, err := app.KubeClient.CoreV1().ConfigMaps(autoscalerStatusNamespace).Get(ctx, autoscalerStatusConfigMap, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return AutoscalerState{NotFound: true}
	}
	if err != nil {
		return AutoscalerState{Unavailable: true}
	}
	return AutoscalerState{Activity: autoscalerActivity(cm.Data)}
}

func pendingPodCount(ctx context.Context, app *App) (int, error) {
	pods, err := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Pending",
	})
	if err != nil {
		return 0, err
	}
	return len(pods.Items), nil
}

func NodeUtilizationFor(node corev1.Node, usage map[string]*metricsv1beta1.NodeMetrics) NodeUtilization {
	metrics, ok := usage[node.Name]
	if !ok {
		return NodeUtilization{}
	}
	cpuUsed := metrics.Usage.Cpu().MilliValue()
	cpuAlloc := node.Status.Allocatable.Cpu().MilliValue()
	memUsed := metrics.Usage.Memory().Value()
	memAlloc := node.Status.Allocatable.Memory().Value()
	if cpuAlloc == 0 || memAlloc == 0 {
		return NodeUtilization{}
	}
	return NodeUtilization{
		CPUPercent: int(cpuUsed * 100 / cpuAlloc),
		MemPercent: int(memUsed * 100 / memAlloc),
		Present:    true,
	}
}

func clusterScore(sumPercent, count int) float64 {
	if count == 0 {
		return 0.0
	}
	return float64(sumPercent) / float64(count) / 100.0
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

func ReplicaCell(n *int32) string {
	if n == nil {
		return "0"
	}
	return fmt.Sprintf("%d", *n)
}

func ValueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return emptyCell
	}
	return s
}

func BoolOrDash(b *bool) string {
	if b == nil {
		return emptyCell
	}
	return fmt.Sprintf("%t", *b)
}

func GetNodeIPv4(node corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP && !strings.Contains(addr.Address, ":") {
			return addr.Address
		}
	}
	return "N/A"
}

func NodeRole(node corev1.Node) string {
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return "master"
	}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return "master"
	}
	return "worker"
}

func NodeStatus(node corev1.Node) string {
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
