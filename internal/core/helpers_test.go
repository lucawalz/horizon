package core_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/provider"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeProvider struct {
	servers []provider.Server
	nextID  int64
}

func newFakeProvider(seed ...provider.Server) *fakeProvider {
	p := &fakeProvider{servers: append([]provider.Server(nil), seed...)}
	for _, s := range seed {
		if s.ID > p.nextID {
			p.nextID = s.ID
		}
	}
	return p
}

func (p *fakeProvider) ListReservedServers(context.Context) ([]provider.Server, error) {
	return append([]provider.Server(nil), p.servers...), nil
}

func (p *fakeProvider) ScaleReservedTo(_ context.Context, want int) (int, error) {
	for len(p.servers) < want {
		p.nextID++
		p.servers = append(p.servers, provider.Server{ID: p.nextID, Name: fmt.Sprintf("reserved-%d", p.nextID)})
	}
	if want < len(p.servers) {
		p.servers = p.servers[:want]
	}
	return want, nil
}

func reservedServer(id int64, name string) provider.Server {
	return provider.Server{
		ID:   id,
		Name: name,
		Labels: map[string]string{
			provider.PoolLabelKey: provider.ReservedPoolValue,
		},
	}
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

func capiScheme(t *testing.T) *crfake.ClientBuilder {
	t.Helper()
	s, err := capi.NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	return crfake.NewClientBuilder().WithScheme(s)
}

func machineDeployment(namespace, name, cluster string, replicas int32) *clusterv1.MachineDeployment {
	return &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: cluster,
			Replicas:    &replicas,
		},
	}
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

func burstCapiClient(t *testing.T, objs ...client.Object) *capi.Client {
	t.Helper()
	cl := capiScheme(t).WithObjects(objs...).Build()
	return capi.NewClientWithCRClient(cl)
}

func noCapiClient(t *testing.T) *capi.Client {
	t.Helper()
	return capi.NewClientWithCRClient(crfake.NewClientBuilder().Build())
}

func collectProgress(msgs *[]string) core.Progress {
	return core.NewProgress(func(msg string) { *msgs = append(*msgs, msg) }, nil)
}
