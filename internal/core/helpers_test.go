package core_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func newFakeProvider(reserved ...string) *fake.Provider {
	p := fake.New()
	for _, name := range reserved {
		p.Seed(provider.Instance{
			Name: name,
			Labels: map[string]string{
				provider.PoolLabelKey:      provider.ReservedPoolValue,
				provider.ManagedByLabelKey: provider.ManagedByValue,
			},
		})
	}
	return p
}

func reservedCount(t *testing.T, p *fake.Provider) int {
	t.Helper()
	instances, err := p.List(context.Background(), map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue})
	if err != nil {
		t.Fatalf("list reserved instances: %v", err)
	}
	return len(instances)
}

func testPoolDefaults() config.PoolDefaults {
	return config.PoolDefaults{
		Namespace:   "caph-system",
		Cluster:     "burst",
		DefaultType: "reserved",
		Types:       map[string]string{"elastic": "elastic-workers", "reserved": "reserved-workers"},
	}
}

func newTestApp() *core.App {
	return &core.App{
		Cluster:       "burst",
		MetricsClient: metricsfake.NewSimpleClientset(),
		FluxClient:    fluxClient(),
		Config: &config.Config{
			Cluster: "burst",
			Pools:   testPoolDefaults(),
		},
	}
}

func reservedTarget(replicas int32) core.PoolTarget {
	return core.PoolTarget{PoolType: "reserved", Cluster: "burst", Replicas: replicas}
}

func nodeWithAllocatable(name, cpu, mem string) *corev1.Node {
	node := readyNode(name)
	node.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
	return node
}

func nodeMetrics(name, cpu, mem string) *metricsv1beta1.NodeMetrics {
	return &metricsv1beta1.NodeMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(mem),
		},
	}
}

func metricsClient(t *testing.T, nm ...*metricsv1beta1.NodeMetrics) *metricsfake.Clientset {
	t.Helper()
	cs := metricsfake.NewSimpleClientset()
	gvr := metricsv1beta1.SchemeGroupVersion.WithResource("nodes")
	for _, m := range nm {
		if err := cs.Tracker().Create(gvr, m, ""); err != nil {
			t.Fatalf("seed node metrics %q: %v", m.Name, err)
		}
	}
	return cs
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func fluxClient(objs ...runtime.Object) *k8s.FluxClient {
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}: "KustomizationList",
		{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}:        "HelmReleaseList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
	return k8s.NewFluxClientWithDynamic(dyn)
}

func collectProgress(msgs *[]string) core.Progress {
	return core.NewProgress(func(msg string) { *msgs = append(*msgs, msg) }, nil)
}
