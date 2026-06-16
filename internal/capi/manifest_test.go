package capi_test

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/yaml"
)

func topologyClusterSpec() capi.ClusterSpec {
	return capi.ClusterSpec{
		Name:                 "edge",
		Namespace:            "caph-system",
		Class:                "hetzner-k3s",
		WorkerClass:          "default-worker",
		WorkerName:           "edge-workers",
		Version:              "v1.35.2+k3s1",
		ControlPlaneReplicas: 1,
		WorkerReplicas:       3,
		Variables: []capi.ClusterVariable{
			{Name: "machineType", Value: "cpx22"},
			{Name: "diskSize", Value: "40"},
			{Name: "config", Value: `{"location":"fsn1"}`},
		},
	}
}

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

func TestRenderClusterTopology(t *testing.T) {
	data, err := capi.RenderCluster(topologyClusterSpec())
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}

	var cluster clusterv1.Cluster
	if err := yaml.Unmarshal(data, &cluster); err != nil {
		t.Fatalf("unmarshal cluster: %v", err)
	}
	if cluster.Kind != "Cluster" {
		t.Errorf("kind = %q, want Cluster", cluster.Kind)
	}
	if got := cluster.Spec.Topology.ClassRef.Name; got != "hetzner-k3s" {
		t.Errorf("topology classRef name = %q, want hetzner-k3s", got)
	}
	if got := cluster.Spec.Topology.Version; got != "v1.35.2+k3s1" {
		t.Errorf("topology version = %q", got)
	}
	if cluster.Spec.Topology.ControlPlane.Replicas == nil || *cluster.Spec.Topology.ControlPlane.Replicas != 1 {
		t.Errorf("control plane replicas = %v, want 1", cluster.Spec.Topology.ControlPlane.Replicas)
	}
	mds := cluster.Spec.Topology.Workers.MachineDeployments
	if len(mds) != 1 {
		t.Fatalf("worker md count = %d, want 1", len(mds))
	}
	if mds[0].Class != "default-worker" || mds[0].Name != "edge-workers" {
		t.Errorf("worker md = %+v", mds[0])
	}
	if mds[0].Replicas == nil || *mds[0].Replicas != 3 {
		t.Errorf("worker replicas = %v, want 3", mds[0].Replicas)
	}
}

func TestRenderClusterTopologyHasNoProviderKinds(t *testing.T) {
	data, err := capi.RenderCluster(topologyClusterSpec())
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	rendered := string(data)
	for _, forbidden := range []string{"HetznerCluster", "HCloudMachineTemplate", "KThreesControlPlane", "KThreesConfigTemplate", "infrastructure.cluster.x-k8s.io"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("topology cluster must not contain provider kind %q:\n%s", forbidden, rendered)
		}
	}
}

func TestRenderClusterEncodesVariables(t *testing.T) {
	data, err := capi.RenderCluster(topologyClusterSpec())
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	var cluster clusterv1.Cluster
	if err := yaml.Unmarshal(data, &cluster); err != nil {
		t.Fatalf("unmarshal cluster: %v", err)
	}
	vars := map[string]string{}
	for _, v := range cluster.Spec.Topology.Variables {
		vars[v.Name] = string(v.Value.Raw)
	}
	if vars["machineType"] != `"cpx22"` {
		t.Errorf("machineType encoded = %s, want quoted string", vars["machineType"])
	}
	if vars["diskSize"] != "40" {
		t.Errorf("diskSize encoded = %s, want bare number 40", vars["diskSize"])
	}
	if vars["config"] != `{"location":"fsn1"}` {
		t.Errorf("config encoded = %s, want passthrough object", vars["config"])
	}
}

func TestRenderClusterOmitsWorkerWhenZeroReplicas(t *testing.T) {
	spec := topologyClusterSpec()
	spec.WorkerReplicas = 0
	data, err := capi.RenderCluster(spec)
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	var cluster clusterv1.Cluster
	if err := yaml.Unmarshal(data, &cluster); err != nil {
		t.Fatalf("unmarshal cluster: %v", err)
	}
	if len(cluster.Spec.Topology.Workers.MachineDeployments) != 0 {
		t.Errorf("expected no worker machine deployments, got %d", len(cluster.Spec.Topology.Workers.MachineDeployments))
	}
}
