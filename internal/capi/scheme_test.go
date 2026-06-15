package capi

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewScheme(t *testing.T) {
	scheme, err := NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	if scheme == nil {
		t.Fatal("NewScheme returned nil scheme")
	}
}

func TestSchemeRoundTrip(t *testing.T) {
	scheme, err := NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}

	want := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "md1", Namespace: "default"},
		Spec:       clusterv1.MachineDeploymentSpec{ClusterName: "cluster1"},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewClientWithCRClient(cl)

	ctx := context.Background()
	if err := c.crClient().Create(ctx, want); err != nil {
		t.Fatalf("create MachineDeployment: %v", err)
	}

	var got clusterv1.MachineDeployment
	key := types.NamespacedName{Name: "md1", Namespace: "default"}
	if err := c.crClient().Get(ctx, key, &got); err != nil {
		t.Fatalf("get MachineDeployment: %v", err)
	}
	if got.Spec.ClusterName != "cluster1" {
		t.Errorf("ClusterName = %q, want cluster1", got.Spec.ClusterName)
	}
}

func TestNewClientWithCRClient(t *testing.T) {
	scheme, err := NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewClientWithCRClient(cl)
	if c.crClient() != cl {
		t.Error("NewClientWithCRClient did not wrap the provided client")
	}
}
