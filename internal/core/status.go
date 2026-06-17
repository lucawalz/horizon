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
	Workload    WorkloadSummary
	NodeHealth  NodeHealthSummary
	Flux        FluxSummary
}

func BuildSnapshot(ctx context.Context, app *App) Snapshot {
	usage, usageErr := FetchNodeUsage(ctx, app)

	nodes, nodesErr := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	pods, podsErr := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{})

	snap := Snapshot{
		Nudge:      NudgeStateFor(ctx, app),
		Autoscaler: AutoscalerStateFor(ctx, app),
	}

	var nodeReady map[string]bool
	if nodesErr != nil {
		snap.Pressure = PressureSummary{Err: nodesErr}
		snap.NodesErr = nodesErr
	} else {
		snap.Pressure = pressureFromLists(nodes.Items, pods, podsErr, usage)
		snap.Nodes = nodeRowsFromLists(nodes.Items, pods, podsErr, usage)
		nodeReady = nodeReadyMap(nodes.Items)
	}
	if usageErr != nil {
		snap.Pressure.MetricsUnavailable = usageErr
	}

	if podsErr != nil {
		snap.Workload = WorkloadSummary{Err: podsErr}
	} else {
		deps, sts, ds, wErr := workloadKindsFromAPI(ctx, app)
		snap.Workload = workloadFromLists(pods, deps, sts, ds)
		if wErr != nil {
			snap.Workload.Err = wErr
		}
	}
	if nodesErr != nil {
		snap.NodeHealth = NodeHealthSummary{Err: nodesErr}
	} else {
		snap.NodeHealth = nodeHealthFromLists(nodes.Items, pods)
	}
	snap.Flux = fluxSummary(ctx, app)

	snap.Pools, snap.PoolsErr = PoolRows(ctx, app, nodeReady)
	snap.Clusters, snap.ClustersErr = ClusterRows(ctx, app)
	return snap
}

func podCountsByNode(pods *corev1.PodList) map[string]int {
	counts := make(map[string]int)
	if pods == nil {
		return counts
	}
	for i := range pods.Items {
		counts[pods.Items[i].Spec.NodeName]++
	}
	return counts
}

func countPendingPods(pods *corev1.PodList) int {
	if pods == nil {
		return 0
	}
	pending := 0
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodPending {
			pending++
		}
	}
	return pending
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
	pods, podsErr := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	return pressureFromLists(nodes.Items, pods, podsErr, usage)
}

func pressureFromLists(nodes []corev1.Node, pods *corev1.PodList, podsErr error, usage map[string]*metricsv1beta1.NodeMetrics) PressureSummary {
	var cpuFraction, memFraction float64
	var ready int
	for _, node := range nodes {
		if NodeStatus(node) != "Ready" {
			continue
		}
		frac, ok := nodeUsageFraction(node, usage)
		if !ok {
			continue
		}
		cpuFraction += frac.cpu
		memFraction += frac.mem
		ready++
	}

	return PressureSummary{
		Available:      true,
		CPUScore:       clusterScore(cpuFraction, ready),
		MemScore:       clusterScore(memFraction, ready),
		PendingPods:    countPendingPods(pods),
		MetricsWarning: podsErr,
	}
}

type usageFraction struct {
	cpu float64
	mem float64
}

func nodeUsageFraction(node corev1.Node, usage map[string]*metricsv1beta1.NodeMetrics) (usageFraction, bool) {
	metrics, ok := usage[node.Name]
	if !ok {
		return usageFraction{}, false
	}
	cpuAlloc := node.Status.Allocatable.Cpu().MilliValue()
	memAlloc := node.Status.Allocatable.Memory().Value()
	if cpuAlloc == 0 || memAlloc == 0 {
		return usageFraction{}, false
	}
	return usageFraction{
		cpu: float64(metrics.Usage.Cpu().MilliValue()) / float64(cpuAlloc),
		mem: float64(metrics.Usage.Memory().Value()) / float64(memAlloc),
	}, true
}

func NodeRows(ctx context.Context, app *App, usage map[string]*metricsv1beta1.NodeMetrics) ([]NodeRow, error) {
	nodes, err := app.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, podsErr := app.KubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	return nodeRowsFromLists(nodes.Items, pods, podsErr, usage), nil
}

func nodeRowsFromLists(nodes []corev1.Node, pods *corev1.PodList, podsErr error, usage map[string]*metricsv1beta1.NodeMetrics) []NodeRow {
	var counts map[string]int
	if podsErr == nil {
		counts = podCountsByNode(pods)
	}
	rows := make([]NodeRow, 0, len(nodes))
	for _, node := range nodes {
		util := NodeUtilizationFor(node, usage)
		rows = append(rows, NodeRow{
			Name:           node.Name,
			Role:           NodeRole(node),
			CPUPercent:     util.CPUPercent,
			MemPercent:     util.MemPercent,
			MetricsPresent: util.Present,
			PodCount:       counts[node.Name],
			Status:         NodeStatus(node),
			IPv4:           GetNodeIPv4(node),
		})
	}
	return rows
}

func PoolRows(ctx context.Context, app *App, nodeReady map[string]bool) ([]PoolRow, error) {
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
		}
		machines, err := app.CapiClient.ListMachines(ctx, app.Config.Pools.Namespace, pool.Name)
		if err != nil {
			row.Ready = "0"
			row.Machines = []MachineRow{{Err: err}}
			rows = append(rows, row)
			continue
		}
		ready := 0
		for _, m := range machines {
			node := m.Status.NodeRef.Name
			if node != "" && nodeReady[node] {
				ready++
			}
			row.Machines = append(row.Machines, MachineRow{
				Name:       m.Name,
				Phase:      m.Status.Phase,
				Node:       node,
				ProviderID: m.Spec.ProviderID,
			})
		}
		row.Ready = fmt.Sprintf("%d", ready)
		rows = append(rows, row)
	}
	return rows, nil
}

func nodeReadyMap(nodes []corev1.Node) map[string]bool {
	ready := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		ready[node.Name] = NodeStatus(node) == "Ready"
	}
	return ready
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

func NodeUtilizationFor(node corev1.Node, usage map[string]*metricsv1beta1.NodeMetrics) NodeUtilization {
	frac, ok := nodeUsageFraction(node, usage)
	if !ok {
		return NodeUtilization{}
	}
	return NodeUtilization{
		CPUPercent: int(frac.cpu * 100),
		MemPercent: int(frac.mem * 100),
		Present:    true,
	}
}

func clusterScore(sumFraction float64, count int) float64 {
	if count == 0 {
		return 0.0
	}
	return sumFraction / float64(count)
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
