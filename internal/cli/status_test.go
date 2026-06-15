package cli_test

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/prometheus/common/model"
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
	md := machineDeployment(namespace, name, cluster, desired)
	md.Labels = map[string]string{"horizon.dev/managed-by": "horizon"}
	md.Status.ReadyReplicas = &ready
	return md
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
	md := mdWithStatus("caph-system", "burst-workers", "burst", 2, 1)
	running := machineFor("caph-system", "burst-workers", "m-running", "Running", "node-a", "hcloud://123")
	provisioning := machineFor("caph-system", "burst-workers", "m-provisioning", "Provisioning", "", "")

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = capiClient(t, md, running, provisioning,
		initializedCluster("caph-system", "burst", true))

	out := runStatus(t, app)

	for _, want := range []string{
		"POOL", "DESIRED", "READY", "PROVIDER-ID",
		"burst-workers", "m-running", "Running", "node-a", "hcloud://123",
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

func sampleAt(instance string, value float64) *model.Sample {
	return &model.Sample{
		Metric: model.Metric{"instance": model.LabelValue(instance)},
		Value:  model.SampleValue(value),
	}
}

func TestNodeMetricCell(t *testing.T) {
	vec := model.Vector{
		sampleAt("192.168.2.191:9100", 0.14),
		sampleAt("192.168.2.100:9100", 0.5),
		sampleAt("192.168.2.207:9100", math.NaN()),
	}

	cases := []struct {
		name   string
		nodeIP string
		want   string
	}{
		{"matched", "192.168.2.191", "14%"},
		{"matched rounds", "192.168.2.100", "50%"},
		{"nan falls soft", "192.168.2.207", "N/A"},
		{"no series", "192.168.2.42", "N/A"},
		{"empty ip", "", "N/A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.NodeMetricCellForTest(vec, tc.nodeIP); got != tc.want {
				t.Errorf("NodeMetricCell(%q) = %q; want %q", tc.nodeIP, got, tc.want)
			}
		})
	}
}
