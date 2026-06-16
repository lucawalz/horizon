package core_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func kubeWithNamespaces(t *testing.T, names ...string) *fake.Clientset {
	t.Helper()
	objs := make([]runtime.Object, len(names))
	for i, n := range names {
		objs[i] = namespace(n)
	}
	return fake.NewSimpleClientset(objs...)
}

func TestDetectPicksBusiestNamespace(t *testing.T) {
	kube := kubeWithNamespaces(t, "default", "caph-system", "kube-system")
	objs := []client.Object{
		mdWithType("caph-system", "elastic-workers", "burst", "elastic", 2, 2),
		mdWithType("caph-system", "reserved-workers", "burst", "reserved", 1, 1),
		mdWithType("other", "stray-workers", "edge", "elastic", 1, 1),
	}
	cc := burstCapiClient(t, objs...)

	got, err := core.Detect(context.Background(), kube, cc)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.PoolsNamespace != "caph-system" {
		t.Errorf("PoolsNamespace = %q, want caph-system", got.PoolsNamespace)
	}
	wantTypes := map[string]string{"elastic": "elastic-workers", "reserved": "reserved-workers"}
	if !reflect.DeepEqual(got.PoolTypes, wantTypes) {
		t.Errorf("PoolTypes = %v, want %v", got.PoolTypes, wantTypes)
	}
	if got.ClusterName != "burst" {
		t.Errorf("ClusterName = %q, want burst", got.ClusterName)
	}
	wantNs := []string{"caph-system", "default", "kube-system"}
	if !reflect.DeepEqual(got.Namespaces, wantNs) {
		t.Errorf("Namespaces = %v, want %v", got.Namespaces, wantNs)
	}
}

func TestDetectNoPools(t *testing.T) {
	kube := kubeWithNamespaces(t, "default", "kube-system")
	cc := burstCapiClient(t)

	got, err := core.Detect(context.Background(), kube, cc)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.PoolsNamespace != "" {
		t.Errorf("PoolsNamespace = %q, want empty", got.PoolsNamespace)
	}
	if len(got.PoolTypes) != 0 {
		t.Errorf("PoolTypes = %v, want empty", got.PoolTypes)
	}
	if got.ClusterName != "" {
		t.Errorf("ClusterName = %q, want empty", got.ClusterName)
	}
	wantNs := []string{"default", "kube-system"}
	if !reflect.DeepEqual(got.Namespaces, wantNs) {
		t.Errorf("Namespaces = %v, want %v", got.Namespaces, wantNs)
	}
}

func TestDetectSurfacesClusters(t *testing.T) {
	kube := kubeWithNamespaces(t, "default")
	objs := []client.Object{
		managedCluster("caph-system", "edge-east", "Provisioned", true),
		managedCluster("caph-system", "edge-west", "Provisioned", true),
	}
	cc := burstCapiClient(t, objs...)

	got, err := core.Detect(context.Background(), kube, cc)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	wantClusters := []string{"edge-east", "edge-west"}
	if !reflect.DeepEqual(got.Clusters, wantClusters) {
		t.Errorf("Clusters = %v, want %v", got.Clusters, wantClusters)
	}
}
