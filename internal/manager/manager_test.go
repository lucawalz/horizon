package manager

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

func TestSchemeRegistersTheHorizonTypes(t *testing.T) {
	s := Scheme()
	for _, kind := range []string{"CapacityLease", "ProviderConfig"} {
		if _, err := s.New(v1alpha1.GroupVersion.WithKind(kind)); err != nil {
			t.Errorf("kind %s not registered: %v", kind, err)
		}
	}
	if _, err := s.New(corev1.SchemeGroupVersion.WithKind("Node")); err != nil {
		t.Errorf("kind Node not registered: %v", err)
	}
}

func TestNodeCacheIsRestrictedToHorizonPools(t *testing.T) {
	var restriction cache.ByObject
	found := false
	for obj, byObject := range cacheOptions().ByObject {
		if _, ok := obj.(*corev1.Node); ok {
			restriction, found = byObject, true
		}
	}
	if !found {
		t.Fatal("node cache carries no restriction")
	}

	if !restriction.Label.Matches(labels.Set{provider.PoolLabelKey: provider.ReservedPoolValue}) {
		t.Errorf("selector %s excludes a pooled node", restriction.Label)
	}
	if restriction.Label.Matches(labels.Set{"kubernetes.io/os": "linux"}) {
		t.Error("selector admits nodes outside every horizon pool")
	}
}

func TestHighCardinalityTypesAreNotCached(t *testing.T) {
	s := Scheme()
	uncached := map[string]bool{}
	for _, obj := range uncachedTypes() {
		kinds, _, err := s.ObjectKinds(obj)
		if err != nil {
			t.Fatalf("resolve kind for %T: %v", obj, err)
		}
		uncached[kinds[0].Kind] = true
	}

	for _, kind := range []string{"Pod", "Secret", "Deployment", "StatefulSet"} {
		if !uncached[kind] {
			t.Errorf("%s is cached cluster-wide", kind)
		}
	}
}
