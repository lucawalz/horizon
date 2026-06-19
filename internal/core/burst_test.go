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

func TestBurstScalesMigratesAndBacksUp(t *testing.T) {
	hostname := "reserved-node-1"
	hc, _ := newHcloudFake(reservedServer(1, hostname))
	kc := fake.NewSimpleClientset(
		poolNode(hostname, "reserved"),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "sentio-systems"}},
		workloadPod("app-1", "sentio-systems", hostname),
	)
	vc := &fakeVeleroClient{}

	target := core.PoolTarget{PoolType: "reserved", Cluster: "burst", Replicas: 1}
	params := core.BurstParams{Target: target, Workload: "sentio-systems", PoolNode: "reserved"}
	if err := core.Burst(context.Background(), hc, reservedSpec(), kc, vc, params, core.Progress{}); err != nil {
		t.Fatalf("Burst: %v", err)
	}

	if !vc.waited {
		t.Error("burst must trigger a velero backup")
	}
	if got := vc.triggeredBackupSpec.IncludedNamespaces; len(got) != 1 || got[0] != "sentio-systems" {
		t.Errorf("backup IncludedNamespaces = %v, want [sentio-systems]", got)
	}

	servers, err := hc.ListReservedServers(context.Background())
	if err != nil {
		t.Fatalf("ListReservedServers: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("reserved servers = %d, want 1", len(servers))
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
	if req.Key != k8s.PoolLabelKey || req.Values[0] != "reserved" {
		t.Errorf("affinity = %s=%v, want %s=[reserved]", req.Key, req.Values, k8s.PoolLabelKey)
	}
}

func TestBurstRefusesElastic(t *testing.T) {
	hc, _ := newHcloudFake()
	kc := fake.NewSimpleClientset()
	vc := &fakeVeleroClient{}

	target := core.PoolTarget{PoolType: core.ElasticPoolType, Replicas: 1}
	params := core.BurstParams{Target: target, Workload: "sentio-systems", PoolNode: "elastic"}
	err := core.Burst(context.Background(), hc, reservedSpec(), kc, vc, params, core.Progress{})
	if err == nil || !strings.Contains(err.Error(), "elastic") {
		t.Fatalf("expected elastic refusal, got %v", err)
	}
	if vc.waited {
		t.Error("backup must not run for an elastic burst")
	}
}

func TestBurstRollsBackOnLaterStageFailure(t *testing.T) {
	hc, f := newHcloudFake(reservedServer(1, "reserved-node-1"), reservedServer(2, "reserved-node-2"))
	kc := fake.NewSimpleClientset()
	vc := &fakeVeleroClient{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	target := core.PoolTarget{PoolType: "reserved", Cluster: "burst", Replicas: 1}
	params := core.BurstParams{Target: target, Workload: "sentio-systems", PoolNode: "reserved"}
	err := core.Burst(ctx, hc, reservedSpec(), kc, vc, params, core.Progress{})
	if err == nil {
		t.Fatal("expected error when the node wait is cancelled")
	}

	if len(f.servers) != 2 {
		t.Errorf("reserved pool must roll back to prior 2 servers, got %d", len(f.servers))
	}
}
