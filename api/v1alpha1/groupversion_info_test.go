package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToSchemeRegistersAllKinds(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	for _, kind := range []string{"CapacityLease", "CapacityLeaseList", "ProviderConfig", "ProviderConfigList"} {
		if _, err := s.New(GroupVersion.WithKind(kind)); err != nil {
			t.Errorf("kind %s not registered: %v", kind, err)
		}
	}
}
