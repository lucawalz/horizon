package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGracefulCommandContext_SendsSIGTERMNotSIGKILL(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term.marker")
	script := "trap 'echo got-term > " + marker + "; exit 0' TERM; echo ready; while true; do sleep 0.05; done"

	ctx, cancel := context.WithCancel(context.Background())
	cmd := cli.GracefulCommandContextForTest(ctx, "sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("marker should not exist before cancel")
		}
		time.Sleep(20 * time.Millisecond)
		break
	}

	cancel()
	_ = cmd.Wait()

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("expected SIGTERM trap to write marker, got: %v (process was likely SIGKILLed)", err)
	}
	if string(data) != "got-term\n" {
		t.Errorf("marker = %q, want \"got-term\\n\"", string(data))
	}
}

func TestBurstSpawnArgs_IncludesBurstID(t *testing.T) {
	args := cli.BurstSpawnArgsForTest("sentio-systems", "5d855091")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--burst-id 5d855091") {
		t.Fatalf("spawn args %v must pass --burst-id so the node name matches the tracked id", args)
	}
	if !strings.Contains(joined, "--workload sentio-systems") {
		t.Errorf("spawn args %v missing --workload", args)
	}
}

func TestBurstSpawnArgs_TrackedIDMatchesNodeName(t *testing.T) {
	id := "5d855091"
	args := cli.BurstSpawnArgsForTest("sentio-systems", id)
	var spawnedID string
	for i, a := range args {
		if a == "--burst-id" && i+1 < len(args) {
			spawnedID = args[i+1]
		}
	}
	if spawnedID != id {
		t.Fatalf("burst subprocess id %q must equal the daemon-tracked id %q", spawnedID, id)
	}
	wantNode := "horizon-burst-" + id
	if got := "horizon-burst-" + spawnedID; got != wantNode {
		t.Errorf("derived node name %q != %q", got, wantNode)
	}
}

func TestRunWatch_HealsStrandedAffinityOnStartup(t *testing.T) {
	ns := "sentio-systems"
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: ns},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{{
							MatchExpressions: []corev1.NodeSelectorRequirement{{
								Key:      k8s.NodeAffinityLabelKey,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{ns},
							}},
						}},
					},
				},
			}},
		}},
	}
	homelab := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "homelab-1"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}

	kc := fake.NewSimpleClientset(dep, homelab)
	deps := cli.WatchDepsForTest{KubeClient: kc, MetricPusher: &mockPusher{}}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	app := &cli.App{Config: &config.Config{}}
	done := make(chan error, 1)
	go func() { done <- cli.RunWatchForTest(ctx, app, deps, ns) }()

	select {
	case err := <-done:
		if err != nil && err != context.DeadlineExceeded {
			t.Errorf("RunWatchForTest: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWatchForTest did not return")
	}

	d, _ := kc.AppsV1().Deployments(ns).Get(context.Background(), "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity != nil && d.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		t.Errorf("watch startup did not heal stranded nodeAffinity: %+v", d.Spec.Template.Spec.Affinity.NodeAffinity)
	}
}
