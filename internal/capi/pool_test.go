package capi_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const poolTypeLabel = "horizon.dev/pool-type"

func mustScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s, err := capi.NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	return s
}

func TestPoolType(t *testing.T) {
	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{poolTypeLabel: "reserved"}},
	}
	if got := capi.PoolType(md); got != "reserved" {
		t.Errorf("PoolType = %q, want reserved", got)
	}
	if got := capi.PoolType(nil); got != "" {
		t.Errorf("PoolType(nil) = %q, want empty", got)
	}
}

func TestListMachineDeploymentsByType(t *testing.T) {
	labeled := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "labeled",
			Namespace: "ns-a",
			Labels:    map[string]string{poolTypeLabel: "reserved"},
		},
		Spec: clusterv1.MachineDeploymentSpec{ClusterName: "homelab"},
	}
	labeledOther := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "labeled-other",
			Namespace: "ns-b",
			Labels:    map[string]string{poolTypeLabel: "elastic"},
		},
		Spec: clusterv1.MachineDeploymentSpec{ClusterName: "homelab"},
	}
	unlabeled := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "unlabeled", Namespace: "ns-a"},
		Spec:       clusterv1.MachineDeploymentSpec{ClusterName: "homelab"},
	}
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).
		WithObjects(labeled, labeledOther, unlabeled).Build()
	c := capi.NewClientWithCRClient(cl)

	inNs, err := c.ListMachineDeploymentsByType(context.Background(), "ns-a")
	if err != nil {
		t.Fatalf("ListMachineDeploymentsByType: %v", err)
	}
	if len(inNs) != 1 || inNs[0].Name != "labeled" {
		t.Fatalf("ns-a results = %v, want [labeled]", inNs)
	}

	all, err := c.ListMachineDeploymentsByType(context.Background(), "")
	if err != nil {
		t.Fatalf("ListMachineDeploymentsByType all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all-namespace count = %d, want 2", len(all))
	}
}
