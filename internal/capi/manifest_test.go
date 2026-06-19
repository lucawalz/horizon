package capi_test

import (
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/yaml"
)

func TestRenderPoolRoundTrips(t *testing.T) {
	data, err := capi.RenderPool(testSpec())
	if err != nil {
		t.Fatalf("RenderPool: %v", err)
	}

	var md clusterv1.MachineDeployment
	if err := yaml.Unmarshal(data, &md); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if md.APIVersion != "cluster.x-k8s.io/v1beta2" {
		t.Errorf("apiVersion = %q, want cluster.x-k8s.io/v1beta2", md.APIVersion)
	}
	if md.Kind != "MachineDeployment" {
		t.Errorf("kind = %q, want MachineDeployment", md.Kind)
	}
	if md.Spec.Replicas == nil || *md.Spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", md.Spec.Replicas)
	}
	if md.Labels[testManagedByLabel] != testManagedByValue {
		t.Errorf("managed-by label = %q, want %q", md.Labels[testManagedByLabel], testManagedByValue)
	}
	if got := md.Spec.Template.Spec.InfrastructureRef.APIGroup; got != "infrastructure.cluster.x-k8s.io" {
		t.Errorf("infra apiGroup = %q", got)
	}
	if got := md.Spec.Template.Spec.Bootstrap.ConfigRef.APIGroup; got != "bootstrap.cluster.x-k8s.io" {
		t.Errorf("bootstrap apiGroup = %q", got)
	}
}
