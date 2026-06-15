package capi_test

import (
	"bytes"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/yaml"
)

func testClusterSpec(mode capi.ControlPlaneMode) capi.ClusterSpec {
	return capi.ClusterSpec{
		Name:             "burst",
		Namespace:        "caph-system",
		ClusterName:      "burst",
		ControlPlaneMode: mode,
		PodCIDR:          "10.42.0.0/16",
		ServiceCIDR:      "10.43.0.0/16",
		Version:          "v1.35.2+k3s1",
		Replicas:         3,
		ClusterInfrastructure: capi.TemplateRef{
			APIGroup: "infrastructure.cluster.x-k8s.io",
			Kind:     "HetznerCluster",
			Name:     "burst",
		},
		Infrastructure: capi.TemplateRef{
			APIGroup: "infrastructure.cluster.x-k8s.io",
			Kind:     "HCloudMachineTemplate",
			Name:     "burst-workers",
		},
		ControlPlaneInfra: capi.TemplateRef{
			APIGroup: "infrastructure.cluster.x-k8s.io",
			Kind:     "HCloudMachineTemplate",
			Name:     "burst-control-plane",
		},
		Bootstrap: capi.TemplateRef{
			APIGroup: "bootstrap.cluster.x-k8s.io",
			Kind:     "KThreesConfigTemplate",
			Name:     "burst",
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

func splitDocs(data []byte) [][]byte {
	parts := bytes.Split(data, []byte("---\n"))
	docs := make([][]byte, 0, len(parts))
	for _, p := range parts {
		if len(bytes.TrimSpace(p)) > 0 {
			docs = append(docs, p)
		}
	}
	return docs
}

func TestRenderClusterExternal(t *testing.T) {
	data, err := capi.RenderCluster(testClusterSpec(capi.External))
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	docs := splitDocs(data)
	if len(docs) != 2 {
		t.Fatalf("doc count = %d, want 2 (cluster + worker md)", len(docs))
	}

	var cluster clusterv1.Cluster
	if err := yaml.Unmarshal(docs[0], &cluster); err != nil {
		t.Fatalf("unmarshal cluster: %v", err)
	}
	if cluster.Kind != "Cluster" {
		t.Errorf("kind = %q, want Cluster", cluster.Kind)
	}
	if cluster.Spec.ControlPlaneRef.Name != "" {
		t.Errorf("external cluster must not set controlPlaneRef, got %q", cluster.Spec.ControlPlaneRef.Name)
	}
	if got := cluster.Spec.InfrastructureRef.Kind; got != "HetznerCluster" {
		t.Errorf("infrastructureRef kind = %q, want HetznerCluster", got)
	}
	if got := cluster.Spec.InfrastructureRef.APIGroup; got != "infrastructure.cluster.x-k8s.io" {
		t.Errorf("infrastructureRef apiGroup = %q, want infrastructure.cluster.x-k8s.io", got)
	}
	if got := cluster.Spec.InfrastructureRef.Name; got != "burst" {
		t.Errorf("infrastructureRef name = %q, want burst", got)
	}
	if got := cluster.Spec.ClusterNetwork.Pods.CIDRBlocks; len(got) != 1 || got[0] != "10.42.0.0/16" {
		t.Errorf("pod cidr = %v", got)
	}

	var md clusterv1.MachineDeployment
	if err := yaml.Unmarshal(docs[1], &md); err != nil {
		t.Fatalf("unmarshal md: %v", err)
	}
	if md.Name != "burst-workers" {
		t.Errorf("worker md name = %q, want burst-workers", md.Name)
	}
	if got := md.Spec.Template.Spec.InfrastructureRef.Kind; got != "HCloudMachineTemplate" {
		t.Errorf("worker md infrastructureRef kind = %q, want HCloudMachineTemplate", got)
	}
}

func TestRenderClusterManaged(t *testing.T) {
	data, err := capi.RenderCluster(testClusterSpec(capi.Managed))
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	docs := splitDocs(data)
	if len(docs) != 3 {
		t.Fatalf("doc count = %d, want 3 (cluster + cp + worker md)", len(docs))
	}

	var cluster clusterv1.Cluster
	if err := yaml.Unmarshal(docs[0], &cluster); err != nil {
		t.Fatalf("unmarshal cluster: %v", err)
	}
	if got := cluster.Spec.ControlPlaneRef.Kind; got != "KThreesControlPlane" {
		t.Errorf("controlPlaneRef kind = %q, want KThreesControlPlane", got)
	}
	if got := cluster.Spec.InfrastructureRef.Kind; got != "HetznerCluster" {
		t.Errorf("infrastructureRef kind = %q, want HetznerCluster", got)
	}

	if !bytes.Contains(docs[1], []byte("kind: KThreesControlPlane")) {
		t.Errorf("second doc is not a KThreesControlPlane:\n%s", docs[1])
	}
	if !bytes.Contains(docs[1], []byte("controlplane.cluster.x-k8s.io/v1beta2")) {
		t.Errorf("control plane apiVersion missing:\n%s", docs[1])
	}
	if !bytes.Contains(docs[1], []byte("kind: HCloudMachineTemplate")) {
		t.Errorf("control plane machineTemplate must reference HCloudMachineTemplate:\n%s", docs[1])
	}
}
