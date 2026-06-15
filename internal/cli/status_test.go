package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func machineFor(namespace, pool, name, phase, node, providerID string) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{clusterv1.MachineDeploymentNameLabel: pool},
		},
	}
	m.Spec.ProviderID = providerID
	m.Status.Phase = phase
	m.Status.NodeRef.Name = node
	return m
}

func mdWithStatus(namespace, name, cluster string, desired, ready int32) *clusterv1.MachineDeployment {
	return mdWithType(namespace, name, cluster, "", desired, ready)
}

func mdWithType(namespace, name, cluster, poolType string, desired, ready int32) *clusterv1.MachineDeployment {
	md := machineDeployment(namespace, name, cluster, desired)
	md.Labels = map[string]string{
		"horizon.dev/managed-by":   "horizon",
		clusterv1.ClusterNameLabel: cluster,
	}
	if poolType != "" {
		md.Labels["horizon.dev/pool-type"] = poolType
	}
	md.Status.ReadyReplicas = &ready
	return md
}

func managedCluster(namespace, name, phase string, initialized bool) *clusterv1.Cluster {
	c := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{"horizon.dev/managed-by": "horizon"},
		},
	}
	c.Status.Phase = phase
	c.Status.Initialization.ControlPlaneInitialized = &initialized
	return c
}

func runStatus(t *testing.T, app *cli.App) string {
	t.Helper()
	var buf bytes.Buffer
	if err := cli.RunStatusForTest(context.Background(), app, &buf); err != nil {
		t.Fatalf("RunStatusForTest: %v", err)
	}
	return buf.String()
}

func TestStatusPoolTable(t *testing.T) {
	reserved := mdWithType("caph-system", "reserved-workers", "burst", "reserved", 2, 1)
	elastic := mdWithType("caph-system", "elastic-workers", "burst", "elastic", 0, 0)
	running := machineFor("caph-system", "reserved-workers", "m-running", "Running", "node-a", "hcloud://123")
	provisioning := machineFor("caph-system", "reserved-workers", "m-provisioning", "Provisioning", "", "")

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, reserved, elastic, running, provisioning,
		initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)

	for _, want := range []string{
		"POOL", "TYPE", "DESIRED", "READY", "PROVIDER-ID",
		"reserved-workers", "reserved", "elastic-workers", "elastic",
		"m-running", "Running", "node-a", "hcloud://123",
		"m-provisioning", "Provisioning",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\t2\t") && !strings.Contains(out, "2") {
		t.Errorf("expected desired replicas; got:\n%s", out)
	}
}

func TestStatusClustersSection(t *testing.T) {
	md := mdWithType("caph-system", "reserved-workers", "burst", "reserved", 1, 1)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md,
		initializedCluster("caph-system", "burst", true),
		managedCluster("caph-system", "edge", "Provisioned", true))

	out := runStatus(t, app)
	for _, want := range []string{"Clusters", "CP-INITIALIZED", "edge", "Provisioned"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected clusters section to contain %q; got:\n%s", want, out)
		}
	}
}

func TestStatusEmptyPoolRendersDashes(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 0, 0)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)
	if !strings.Contains(out, "burst-workers") {
		t.Errorf("expected pool row; got:\n%s", out)
	}
	if !strings.Contains(out, "-") {
		t.Errorf("expected dash cells for machineless pool; got:\n%s", out)
	}
}

func TestStatusNudgeWarningWhenNotInitialized(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 0)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", false))

	out := runStatus(t, app)
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "not marked initialized") {
		t.Errorf("expected nudge warning; got:\n%s", out)
	}
	if !strings.Contains(out, "nudged") {
		t.Errorf("expected mention of nudge remediation; got:\n%s", out)
	}
}

func TestStatusNudgeSkippedWhenClusterMissing(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 1)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md)

	out := runStatus(t, app)
	if strings.Contains(out, "WARNING") {
		t.Errorf("expected graceful skip when cluster absent; got:\n%s", out)
	}
	if !strings.Contains(out, "burst-workers") {
		t.Errorf("expected pool table to still render; got:\n%s", out)
	}
}

func TestStatusAutoscalerLinePresent(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 1)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler-status", Namespace: "kube-system"},
		Data:       map[string]string{"status": "Cluster-wide: healthy\nHealth: ok"},
	}

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(cm)
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)
	if !strings.Contains(out, "autoscaler: Cluster-wide: healthy") {
		t.Errorf("expected autoscaler activity line; got:\n%s", out)
	}
}

func TestStatusAutoscalerMissingDegrades(t *testing.T) {
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 1)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)
	if !strings.Contains(out, "autoscaler: not found") {
		t.Errorf("expected graceful autoscaler degradation; got:\n%s", out)
	}
}

func TestStatusNodeMetricsFromMetricsServer(t *testing.T) {
	known := nodeWithAllocatable("worker-1", "4", "8Gi")
	missing := nodeWithAllocatable("master", "4", "8Gi")
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 1)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(known, missing, pending)
	app.MetricsClient = metricsClient(t, nodeMetrics("worker-1", "1", "2Gi"))
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)

	if !strings.Contains(out, "worker-1") {
		t.Fatalf("expected worker-1 row; got:\n%s", out)
	}
	for _, want := range []string{"worker-1", "25%"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected node usage %q; got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "master") || !strings.Contains(out, "N/A") {
		t.Errorf("expected N/A for node without metrics; got:\n%s", out)
	}
	if !strings.Contains(out, "Pending pods: 1") {
		t.Errorf("expected pending pod count 1; got:\n%s", out)
	}
}

func TestStatusMetricsServerUnavailableDegrades(t *testing.T) {
	node := nodeWithAllocatable("worker-1", "4", "8Gi")
	md := mdWithStatus("caph-system", "burst-workers", "burst", 1, 1)

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(node)
	app.MetricsClient = metricsClient(t)
	app.CapiClient = capiClient(t, md, initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)
	if !strings.Contains(out, "worker-1") || !strings.Contains(out, "N/A") {
		t.Errorf("expected node row with N/A when metrics empty; got:\n%s", out)
	}
}
