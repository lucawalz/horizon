package core_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPoolRowsCarryTypeAndMachines(t *testing.T) {
	reserved := mdWithType("caph-system", "reserved-workers", "burst", "reserved", 3, 0)
	elastic := mdWithType("caph-system", "elastic-workers", "burst", "elastic", 0, 0)
	running := machineFor("caph-system", "reserved-workers", "m-running", "Running", "node-a", "hcloud://123")
	notReady := machineFor("caph-system", "reserved-workers", "m-notready", "Running", "node-b", "hcloud://124")
	provisioning := machineFor("caph-system", "reserved-workers", "m-provisioning", "Provisioning", "", "")

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, reserved, elastic, running, notReady, provisioning,
		initializedCluster("caph-system", "burst", true))

	nodeReady := map[string]bool{"node-a": true, "node-b": false}
	rows, err := core.PoolRows(context.Background(), app, nodeReady)
	if err != nil {
		t.Fatalf("PoolRows: %v", err)
	}

	byName := map[string]core.PoolRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	res, ok := byName["reserved-workers"]
	if !ok {
		t.Fatalf("missing reserved-workers row: %+v", rows)
	}
	if res.Type != "reserved" || res.Desired != "3" || res.Ready != "1" {
		t.Errorf("reserved row = %+v", res)
	}
	if el := byName["elastic-workers"]; el.Type != "elastic" {
		t.Errorf("elastic row type = %q, want elastic", el.Type)
	}

	var found bool
	for _, m := range res.Machines {
		if m.Name == "m-running" && m.Phase == "Running" && m.Node == "node-a" && m.ProviderID == "hcloud://123" {
			found = true
		}
	}
	if !found {
		t.Errorf("running machine row missing: %+v", res.Machines)
	}
}

func TestPoolRowsEmptyPoolHasNoMachines(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 0, 0)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", true))

	rows, err := core.PoolRows(context.Background(), app, nil)
	if err != nil {
		t.Fatalf("PoolRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "burst-workers" {
		t.Fatalf("unexpected pool rows %+v", rows)
	}
	if len(rows[0].Machines) != 0 {
		t.Errorf("expected no machines for empty pool, got %+v", rows[0].Machines)
	}
	if rows[0].Desired != "0" || rows[0].Ready != "0" {
		t.Errorf("expected zero replica cells, got %+v", rows[0])
	}
}

func TestNudgeStateUninitializedWhenControlPlaneNotMarked(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 0)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", false))

	state := core.NudgeStateFor(context.Background(), app)
	if state.Kind != core.NudgeUninitialized {
		t.Errorf("nudge kind = %v, want NudgeUninitialized", state.Kind)
	}
}

func TestNudgeStateNotFoundWhenClusterMissing(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 1)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md)

	state := core.NudgeStateFor(context.Background(), app)
	if state.Kind != core.NudgeNotFound {
		t.Errorf("nudge kind = %v, want NudgeNotFound", state.Kind)
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
	node := nodeWithAllocatable("worker-1", "4", "8Gi")
	md := mdWithType("caph-system", "reserved-workers", "burst", "reserved", 1, 0)
	bound := machineFor("caph-system", "reserved-workers", "m-bound", "Running", "worker-1", "hcloud://1")

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(node)
	app.MetricsClient = metricsClient(t, nodeMetrics("worker-1", "1", "2Gi"))
	app.CapiClient = capiClient(t, md, bound, initializedCluster("caph-system", "burst", true))

	snap := core.BuildSnapshot(context.Background(), app)
	if snap.NodesErr != nil || len(snap.Nodes) != 1 {
		t.Errorf("nodes section = %+v err=%v", snap.Nodes, snap.NodesErr)
	}
	if snap.PoolsErr != nil || len(snap.Pools) != 1 {
		t.Errorf("pools section = %+v err=%v", snap.Pools, snap.PoolsErr)
	}
	if snap.Pools[0].Ready != "1" {
		t.Errorf("pool ready = %q, want 1 from ready node", snap.Pools[0].Ready)
	}
	if snap.Nudge.Kind != core.NudgeInitialized {
		t.Errorf("nudge = %v, want NudgeInitialized", snap.Nudge.Kind)
	}
	if !snap.Autoscaler.NotFound {
		t.Errorf("autoscaler = %+v, want NotFound", snap.Autoscaler)
	}
}
