package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func workloadPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func poolNode(name, pool string) *corev1.Node {
	n := readyNode(name)
	n.Labels = map[string]string{k8s.PoolLabelKey: pool}
	return n
}

func readyMachine(namespace, pool, nodeName string) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeName,
			Namespace: namespace,
			Labels:    map[string]string{clusterv1.MachineDeploymentNameLabel: pool},
		},
	}
	m.Status.NodeRef.Name = nodeName
	return m
}

func TestBurstScalesMigratesAndBacksUp(t *testing.T) {
	hostname := "burst-node-1"
	cc := burstCapiClient(
		t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		readyMachine("caph-system", "burst-workers", hostname),
	)
	kc := fake.NewSimpleClientset(
		poolNode(hostname, "burst"),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "sentio-systems"}},
		workloadPod("app-1", "sentio-systems", hostname),
	)
	vc := &fakeVeleroClient{}

	target := poolTarget("caph-system", "burst-workers", "burst", 1)
	params := core.BurstParams{Target: target, Workload: "sentio-systems", PoolNode: "burst"}
	if err := core.Burst(context.Background(), cc, kc, vc, params, core.Progress{}); err != nil {
		t.Fatalf("Burst: %v", err)
	}

	if !vc.waited {
		t.Error("burst must trigger a velero backup")
	}
	if got := vc.triggeredBackupSpec.IncludedNamespaces; len(got) != 1 || got[0] != "sentio-systems" {
		t.Errorf("backup IncludedNamespaces = %v, want [sentio-systems]", got)
	}

	pool, err := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if pool.Spec.Replicas == nil || *pool.Spec.Replicas != 1 {
		t.Errorf("replicas = %v, want 1", pool.Spec.Replicas)
	}

	dep, err := kc.AppsV1().Deployments("sentio-systems").Get(context.Background(), "app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	a := dep.Spec.Template.Spec.Affinity
	if a == nil || a.NodeAffinity == nil || a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatal("workload affinity not set after migrate")
	}
	req := a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0]
	if req.Key != k8s.PoolLabelKey || req.Values[0] != "burst" {
		t.Errorf("affinity = %s=%v, want %s=[burst]", req.Key, req.Values, k8s.PoolLabelKey)
	}
}

func TestBurstFailsFastWhenPoolMissing(t *testing.T) {
	cc := burstCapiClient(t)
	kc := fake.NewSimpleClientset()
	vc := &fakeVeleroClient{}

	target := poolTarget("caph-system", "burst-workers", "burst", 1)
	params := core.BurstParams{Target: target, Workload: "sentio-systems", PoolNode: "burst"}
	err := core.Burst(context.Background(), cc, kc, vc, params, core.Progress{})
	if err == nil {
		t.Fatal("expected fail-fast when pool missing")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "bedrock") {
		t.Errorf("error %q should explain the home pool is GitOps-managed", err.Error())
	}
	if vc.waited {
		t.Error("backup must not run when the pool is missing")
	}
}

func TestBurstRollsBackOnMigrateFailure(t *testing.T) {
	cc := burstCapiClient(
		t,
		machineDeployment("caph-system", "burst-workers", "burst", 2),
		readyMachine("caph-system", "burst-workers", "burst-node-1"),
	)
	kc := fake.NewSimpleClientset()
	vc := &fakeVeleroClient{}

	target := poolTarget("caph-system", "burst-workers", "burst", 1)
	params := core.BurstParams{Target: target, Workload: "sentio-systems", PoolNode: "burst"}
	err := core.Burst(context.Background(), cc, kc, vc, params, core.Progress{})
	if err == nil {
		t.Fatal("expected error when migrate finds no pool node")
	}

	pool, gerr := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if gerr != nil {
		t.Fatalf("GetPool: %v", gerr)
	}
	if pool.Spec.Replicas == nil || *pool.Spec.Replicas != 2 {
		t.Errorf("pool must roll back to prior 2 replicas, got %v", pool.Spec.Replicas)
	}
}
