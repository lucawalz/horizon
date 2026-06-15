package capi_test

import (
	"context"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/capi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func machineWithNode(name, node string) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "caph-system",
			Labels:    map[string]string{clusterv1.MachineDeploymentNameLabel: "burst-workers"},
		},
		Spec: clusterv1.MachineSpec{ClusterName: "burst"},
	}
	if node != "" {
		m.Status.NodeRef = clusterv1.MachineNodeReference{Name: node}
	}
	return m
}

func TestMachineNodeNames(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithScheme(mustScheme(t)).
		WithObjects(machineWithNode("burst-1", "node-1"), machineWithNode("burst-2", "")).
		Build()
	c := capi.NewClientWithCRClient(cl)

	names, err := c.MachineNodeNames(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("MachineNodeNames: %v", err)
	}
	if len(names) != 1 || names[0] != "node-1" {
		t.Errorf("names = %v, want [node-1]", names)
	}
}

func TestWaitMachinesReadySucceeds(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithScheme(mustScheme(t)).
		WithObjects(machineWithNode("burst-1", "node-1"), machineWithNode("burst-2", "node-2")).
		Build()
	c := capi.NewClientWithCRClient(cl)

	if err := c.WaitMachinesReady(context.Background(), "caph-system", "burst-workers", 2, time.Millisecond, time.Second); err != nil {
		t.Fatalf("WaitMachinesReady: %v", err)
	}
}

func TestWaitMachinesReadyTimesOut(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithScheme(mustScheme(t)).
		WithObjects(machineWithNode("burst-1", "node-1"), machineWithNode("burst-2", "")).
		Build()
	c := capi.NewClientWithCRClient(cl)

	err := c.WaitMachinesReady(context.Background(), "caph-system", "burst-workers", 2, time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("WaitMachinesReady: want timeout error, got nil")
	}
}
