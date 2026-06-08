package cli_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStatusBurstPhase(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "horizon-state", Namespace: "kube-system"},
		Data:       map[string]string{"burst_phase": "Migrating"},
	}
	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(cm)

	out := captureStdout(func() {
		cli.PrintBurstPhaseForTest(context.Background(), app)
	})
	if !strings.Contains(out, "BurstPhase: Migrating") {
		t.Errorf("expected output to contain 'BurstPhase: Migrating'; got:\n%s", out)
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

func TestStatusBurstPhase_FallbackIdle(t *testing.T) {
	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()

	out := captureStdout(func() {
		cli.PrintBurstPhaseForTest(context.Background(), app)
	})
	if !strings.Contains(out, "BurstPhase: Idle") {
		t.Errorf("expected output to contain 'BurstPhase: Idle' fallback; got:\n%s", out)
	}
}
