package capi_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testPoolNameLabel  = "horizon.dev/pool-name"
	testManagedByLabel = "horizon.dev/managed-by"
	testManagedByValue = "horizon"
)

func mustScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s, err := capi.NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	return s
}

func testSpec() capi.PoolSpec {
	return capi.PoolSpec{
		Name:        "burst",
		Namespace:   "default",
		ClusterName: "homelab",
		Replicas:    2,
		Version:     "v1.31.0",
		Infrastructure: capi.TemplateRef{
			APIGroup: "infrastructure.cluster.x-k8s.io",
			Kind:     "HCloudMachineTemplate",
			Name:     "burst-infra",
		},
		Bootstrap: capi.TemplateRef{
			APIGroup: "bootstrap.cluster.x-k8s.io",
			Kind:     "KThreesConfigTemplate",
			Name:     "burst-bootstrap",
		},
	}
}

func TestBuildMachineDeployment(t *testing.T) {
	md := capi.BuildMachineDeployment(testSpec())

	if md.Labels[testPoolNameLabel] != "burst" {
		t.Errorf("pool-name label = %q, want burst", md.Labels[testPoolNameLabel])
	}
	if md.Labels[testManagedByLabel] != testManagedByValue {
		t.Errorf("managed-by label = %q, want %q", md.Labels[testManagedByLabel], testManagedByValue)
	}
	if md.Labels[clusterv1.ClusterNameLabel] != "homelab" {
		t.Errorf("cluster-name label = %q, want homelab", md.Labels[clusterv1.ClusterNameLabel])
	}
	if md.Spec.ClusterName != "homelab" {
		t.Errorf("spec.ClusterName = %q, want homelab", md.Spec.ClusterName)
	}
	if md.Spec.Replicas == nil || *md.Spec.Replicas != 2 {
		t.Errorf("spec.Replicas = %v, want 2", md.Spec.Replicas)
	}
	if md.Spec.Template.Spec.Version != "v1.31.0" {
		t.Errorf("template version = %q, want v1.31.0", md.Spec.Template.Spec.Version)
	}
	if got := md.Spec.Template.Spec.Bootstrap.ConfigRef.Kind; got != "KThreesConfigTemplate" {
		t.Errorf("bootstrap kind = %q, want KThreesConfigTemplate", got)
	}
	if got := md.Spec.Template.Spec.Bootstrap.ConfigRef.APIGroup; got != "bootstrap.cluster.x-k8s.io" {
		t.Errorf("bootstrap apiGroup = %q, want bootstrap.cluster.x-k8s.io", got)
	}
	if got := md.Spec.Template.Spec.InfrastructureRef.Name; got != "burst-infra" {
		t.Errorf("infra ref name = %q, want burst-infra", got)
	}
	if got := md.Spec.Template.Spec.InfrastructureRef.APIGroup; got != "infrastructure.cluster.x-k8s.io" {
		t.Errorf("infra ref apiGroup = %q, want infrastructure.cluster.x-k8s.io", got)
	}
	for k, v := range md.Spec.Selector.MatchLabels {
		if md.Spec.Template.Labels[k] != v {
			t.Errorf("selector label %q=%q not matched in template labels", k, v)
		}
	}
	if _, ok := md.Annotations["cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"]; ok {
		t.Error("autoscaler min-size annotation must not be set")
	}
	if _, ok := md.Annotations["cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"]; ok {
		t.Error("autoscaler max-size annotation must not be set")
	}
}

func TestApplyPoolCreates(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	md, err := c.ApplyPool(context.Background(), testSpec())
	if err != nil {
		t.Fatalf("ApplyPool: %v", err)
	}
	if md.Spec.Replicas == nil || *md.Spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", md.Spec.Replicas)
	}

	got, err := c.GetPool(context.Background(), "default", "burst")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Labels[testManagedByLabel] != testManagedByValue {
		t.Errorf("managed-by label = %q, want %q", got.Labels[testManagedByLabel], testManagedByValue)
	}
	if got.Spec.Template.Spec.InfrastructureRef.Name != "burst-infra" {
		t.Errorf("infra ref name = %q, want burst-infra", got.Spec.Template.Spec.InfrastructureRef.Name)
	}
}

func TestApplyPoolUpdatesReplicas(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	if _, err := c.ApplyPool(context.Background(), testSpec()); err != nil {
		t.Fatalf("ApplyPool create: %v", err)
	}

	spec := testSpec()
	spec.Replicas = 5
	md, err := c.ApplyPool(context.Background(), spec)
	if err != nil {
		t.Fatalf("ApplyPool update: %v", err)
	}
	if md.Spec.Replicas == nil || *md.Spec.Replicas != 5 {
		t.Errorf("replicas = %v, want 5", md.Spec.Replicas)
	}

	got, err := c.GetPool(context.Background(), "default", "burst")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if *got.Spec.Replicas != 5 {
		t.Errorf("persisted replicas = %d, want 5", *got.Spec.Replicas)
	}
}

func TestScalePool(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	if _, err := c.ApplyPool(context.Background(), testSpec()); err != nil {
		t.Fatalf("ApplyPool: %v", err)
	}
	if err := c.ScalePool(context.Background(), "default", "burst", 7); err != nil {
		t.Fatalf("ScalePool: %v", err)
	}
	got, err := c.GetPool(context.Background(), "default", "burst")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 7 {
		t.Errorf("replicas = %v, want 7", got.Spec.Replicas)
	}
}

func TestListPoolsFiltersManaged(t *testing.T) {
	managed := capi.BuildMachineDeployment(testSpec())
	unmanaged := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"},
		Spec:       clusterv1.MachineDeploymentSpec{ClusterName: "homelab"},
	}
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).WithObjects(managed, unmanaged).Build()
	c := capi.NewClientWithCRClient(cl)

	pools, err := c.ListPools(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("pool count = %d, want 1", len(pools))
	}
	if pools[0].Name != "burst" {
		t.Errorf("pool name = %q, want burst", pools[0].Name)
	}
}

func TestGetPoolNotFound(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	_, err := c.GetPool(context.Background(), "default", "missing")
	if !apierrors.IsNotFound(err) {
		t.Errorf("GetPool error = %v, want IsNotFound", err)
	}
}

func TestDeletePool(t *testing.T) {
	managed := capi.BuildMachineDeployment(testSpec())
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).WithObjects(managed).Build()
	c := capi.NewClientWithCRClient(cl)

	if err := c.DeletePool(context.Background(), "default", "burst"); err != nil {
		t.Fatalf("DeletePool: %v", err)
	}
	if _, err := c.GetPool(context.Background(), "default", "burst"); !apierrors.IsNotFound(err) {
		t.Errorf("GetPool after delete error = %v, want IsNotFound", err)
	}
}

func TestListMachines(t *testing.T) {
	matching := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "burst-abc",
			Namespace: "default",
			Labels:    map[string]string{clusterv1.MachineDeploymentNameLabel: "burst"},
		},
		Spec: clusterv1.MachineSpec{ClusterName: "homelab"},
	}
	other := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-xyz",
			Namespace: "default",
			Labels:    map[string]string{clusterv1.MachineDeploymentNameLabel: "other"},
		},
		Spec: clusterv1.MachineSpec{ClusterName: "homelab"},
	}
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).WithObjects(matching, other).Build()
	c := capi.NewClientWithCRClient(cl)

	machines, err := c.ListMachines(context.Background(), "default", "burst")
	if err != nil {
		t.Fatalf("ListMachines: %v", err)
	}
	if len(machines) != 1 {
		t.Fatalf("machine count = %d, want 1", len(machines))
	}
	if machines[0].Name != "burst-abc" {
		t.Errorf("machine name = %q, want burst-abc", machines[0].Name)
	}
}
