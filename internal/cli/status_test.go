package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
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
