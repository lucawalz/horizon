package capi_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestApplyClusterTopology(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)
	spec := topologyClusterSpec()

	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster create: %v", err)
	}
	got, err := c.GetCluster(context.Background(), spec.Namespace, spec.Name)
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.Spec.Topology.ClassRef.Name != "hetzner-k3s" {
		t.Errorf("topology classRef name = %q, want hetzner-k3s", got.Spec.Topology.ClassRef.Name)
	}

	spec.WorkerReplicas = 5
	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster update: %v", err)
	}
	got, err = c.GetCluster(context.Background(), spec.Namespace, spec.Name)
	if err != nil {
		t.Fatalf("GetCluster after update: %v", err)
	}
	mds := got.Spec.Topology.Workers.MachineDeployments
	if len(mds) != 1 || mds[0].Replicas == nil || *mds[0].Replicas != 5 {
		t.Errorf("worker replicas after update = %+v, want 5", mds)
	}
}

func TestListClusters(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)
	spec := topologyClusterSpec()
	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}

	clusters, err := c.ListClusters(context.Background(), spec.Namespace)
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("ListClusters returned %d clusters, want 1", len(clusters))
	}
	if clusters[0].Name != spec.Name {
		t.Errorf("cluster name = %q, want %q", clusters[0].Name, spec.Name)
	}
}

func TestDeleteCluster(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)
	spec := topologyClusterSpec()
	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}
	if err := c.DeleteCluster(context.Background(), spec.Namespace, spec.Name); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, err := c.GetCluster(context.Background(), spec.Namespace, spec.Name); !apierrors.IsNotFound(err) {
		t.Errorf("GetCluster after delete = %v, want NotFound", err)
	}
}

func TestApplyManifestsCreatesAndUpdates(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	data, err := capi.RenderCluster(topologyClusterSpec())
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	if err := c.ApplyManifests(context.Background(), data); err != nil {
		t.Fatalf("ApplyManifests create: %v", err)
	}
	if _, err := c.GetCluster(context.Background(), "caph-system", "edge"); err != nil {
		t.Fatalf("GetCluster after apply: %v", err)
	}
	if err := c.ApplyManifests(context.Background(), data); err != nil {
		t.Fatalf("ApplyManifests update: %v", err)
	}
}
