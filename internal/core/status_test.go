package core_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func labelledNode(name, pool string, ready bool) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"horizon.dev/pool": pool}},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func TestPoolRowsGroupNodesByPoolLabel(t *testing.T) {
	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()

	nodes := []corev1.Node{
		*labelledNode("elastic-1", "elastic", true),
		*labelledNode("elastic-2", "elastic", false),
		*labelledNode("reserved-1", "reserved", true),
		*readyNode("master"),
	}

	rows := core.PoolRows(context.Background(), app, nodes)
	byName := map[string]core.PoolRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	el, ok := byName["elastic"]
	if !ok {
		t.Fatalf("missing elastic pool row: %+v", rows)
	}
	if el.Type != "elastic" || el.Desired != "2" || el.Ready != "1" {
		t.Errorf("elastic row = %+v", el)
	}
	res, ok := byName["reserved"]
	if !ok {
		t.Fatalf("missing reserved pool row: %+v", rows)
	}
	if res.Desired != "1" || res.Ready != "1" {
		t.Errorf("reserved row = %+v", res)
	}
	if _, ok := byName["worker"]; ok {
		t.Errorf("unlabelled nodes must not form a pool: %+v", rows)
	}
}

func TestAutoscalerStateActivityFromConfigMap(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler-status", Namespace: "kube-system"},
		Data:       map[string]string{"status": "Cluster-wide: healthy\nHealth: ok"},
	}

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(cm)

	state := core.AutoscalerStateFor(context.Background(), app)
	if state.NotFound || state.Unavailable {
		t.Fatalf("unexpected state %+v", state)
	}
	if state.Activity != "Cluster-wide: healthy" {
		t.Errorf("activity = %q, want Cluster-wide: healthy", state.Activity)
	}
}

func TestAutoscalerStateActivityFromStatusField(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler-status", Namespace: "kube-system"},
		Data:       map[string]string{"status": "time: 2026-06-17\nautoscalerStatus: Running\nclusterName: burst"},
	}

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(cm)

	state := core.AutoscalerStateFor(context.Background(), app)
	if state.Activity != "running" {
		t.Errorf("activity = %q, want running", state.Activity)
	}
}

func TestAutoscalerStateNotFoundDegrades(t *testing.T) {
	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()

	state := core.AutoscalerStateFor(context.Background(), app)
	if !state.NotFound {
		t.Errorf("expected NotFound autoscaler state, got %+v", state)
	}
}

func TestNodeRowsAndPressureFromMetricsServer(t *testing.T) {
	known := nodeWithAllocatable("worker-1", "4", "8Gi")
	missing := nodeWithAllocatable("master", "4", "8Gi")
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(known, missing, pending)
	app.MetricsClient = metricsClient(t, nodeMetrics("worker-1", "1", "2Gi"))

	usage, err := core.FetchNodeUsage(context.Background(), app)
	if err != nil {
		t.Fatalf("FetchNodeUsage: %v", err)
	}

	rows, err := core.NodeRows(context.Background(), app, usage)
	if err != nil {
		t.Fatalf("NodeRows: %v", err)
	}
	byName := map[string]core.NodeRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	w1, ok := byName["worker-1"]
	if !ok {
		t.Fatalf("missing worker-1 row: %+v", rows)
	}
	if !w1.MetricsPresent || w1.CPUPercent != 25 {
		t.Errorf("worker-1 row = %+v, want 25%% cpu present", w1)
	}
	if byName["master"].MetricsPresent {
		t.Errorf("master must lack metrics: %+v", byName["master"])
	}

	p := core.PressureFor(context.Background(), app, usage)
	if !p.Available {
		t.Fatalf("pressure unavailable: %+v", p)
	}
	if p.PendingPods != 1 {
		t.Errorf("pending pods = %d, want 1", p.PendingPods)
	}
}

func TestPressureDegradesWhenMetricsEmpty(t *testing.T) {
	node := nodeWithAllocatable("worker-1", "4", "8Gi")

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(node)
	app.MetricsClient = metricsClient(t)

	usage, err := core.FetchNodeUsage(context.Background(), app)
	if err != nil {
		t.Fatalf("FetchNodeUsage: %v", err)
	}
	rows, err := core.NodeRows(context.Background(), app, usage)
	if err != nil {
		t.Fatalf("NodeRows: %v", err)
	}
	if len(rows) != 1 || rows[0].MetricsPresent {
		t.Errorf("expected single node without metrics, got %+v", rows)
	}
}

func TestBuildSnapshotAssemblesSections(t *testing.T) {
	worker := nodeWithAllocatable("worker-1", "4", "8Gi")
	reserved := labelledNode("reserved-1", "reserved", true)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(worker, reserved)
	app.MetricsClient = metricsClient(t, nodeMetrics("worker-1", "1", "2Gi"))
	app.CapiClient = burstCapiClient(t)

	snap := core.BuildSnapshot(context.Background(), app)
	if snap.NodesErr != nil || len(snap.Nodes) != 2 {
		t.Errorf("nodes section = %+v err=%v", snap.Nodes, snap.NodesErr)
	}
	if snap.PoolsErr != nil || len(snap.Pools) != 1 {
		t.Errorf("pools section = %+v err=%v", snap.Pools, snap.PoolsErr)
	}
	if snap.Pools[0].Name != "reserved" || snap.Pools[0].Ready != "1" {
		t.Errorf("pool row = %+v, want reserved ready 1", snap.Pools[0])
	}
	if !snap.Autoscaler.NotFound {
		t.Errorf("autoscaler = %+v, want NotFound", snap.Autoscaler)
	}
}
