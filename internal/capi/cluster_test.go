package capi_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func controlPlaneGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "controlplane.cluster.x-k8s.io",
		Version: "v1beta2",
		Kind:    "KThreesControlPlane",
	}
}

func TestApplyClusterExternal(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)
	spec := testClusterSpec(capi.External)

	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster create: %v", err)
	}
	got, err := c.GetCluster(context.Background(), spec.Namespace, spec.ClusterName)
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.Spec.ControlPlaneRef.Name != "" {
		t.Errorf("external cluster set controlPlaneRef %q", got.Spec.ControlPlaneRef.Name)
	}

	spec.PodCIDR = "10.99.0.0/16"
	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster update: %v", err)
	}
	got, err = c.GetCluster(context.Background(), spec.Namespace, spec.ClusterName)
	if err != nil {
		t.Fatalf("GetCluster after update: %v", err)
	}
	if got.Spec.ClusterNetwork.Pods.CIDRBlocks[0] != "10.99.0.0/16" {
		t.Errorf("pod cidr = %v, want updated", got.Spec.ClusterNetwork.Pods.CIDRBlocks)
	}

	pool, err := c.GetPool(context.Background(), spec.Namespace, "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if pool.Name != "burst-workers" {
		t.Errorf("worker pool name = %q", pool.Name)
	}
}

func TestApplyClusterManaged(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)
	spec := testClusterSpec(capi.Managed)

	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster create: %v", err)
	}
	got, err := c.GetCluster(context.Background(), spec.Namespace, spec.ClusterName)
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.Spec.ControlPlaneRef.Kind != "KThreesControlPlane" {
		t.Errorf("controlPlaneRef kind = %q", got.Spec.ControlPlaneRef.Kind)
	}

	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(controlPlaneGVK())
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: spec.Namespace, Name: spec.Name}, cp); err != nil {
		t.Fatalf("get control plane: %v", err)
	}

	spec.Replicas = 5
	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster update: %v", err)
	}
	cp = &unstructured.Unstructured{}
	cp.SetGroupVersionKind(controlPlaneGVK())
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: spec.Namespace, Name: spec.Name}, cp); err != nil {
		t.Fatalf("get control plane after update: %v", err)
	}
	replicas, found, err := unstructured.NestedInt64(cp.Object, "spec", "replicas")
	if err != nil || !found {
		t.Fatalf("read replicas: found=%v err=%v", found, err)
	}
	if replicas != 5 {
		t.Errorf("control plane replicas = %d, want 5", replicas)
	}
}

func TestDeleteCluster(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)
	spec := testClusterSpec(capi.External)
	if err := c.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}
	if err := c.DeleteCluster(context.Background(), spec.Namespace, spec.ClusterName); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, err := c.GetCluster(context.Background(), spec.Namespace, spec.ClusterName); !apierrors.IsNotFound(err) {
		t.Errorf("GetCluster after delete = %v, want NotFound", err)
	}
}
