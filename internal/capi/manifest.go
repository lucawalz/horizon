package capi

import (
	"bytes"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/yaml"
)

const (
	clusterAPIVersion        = "cluster.x-k8s.io/v1beta2"
	machineDeploymentKind    = "MachineDeployment"
	clusterKind              = "Cluster"
	kThreesControlPlaneGroup = "controlplane.cluster.x-k8s.io"
	kThreesControlPlaneKind  = "KThreesControlPlane"
	kThreesControlPlaneAPI   = kThreesControlPlaneGroup + "/v1beta2"
)

type ControlPlaneMode int

const (
	External ControlPlaneMode = iota
	Managed
)

type ClusterSpec struct {
	Name                  string
	Namespace             string
	ClusterName           string
	ControlPlaneMode      ControlPlaneMode
	PodCIDR               string
	ServiceCIDR           string
	Version               string
	ClusterInfrastructure TemplateRef
	Infrastructure        TemplateRef
	ControlPlaneInfra     TemplateRef
	Bootstrap             TemplateRef
	Replicas              int32
}

func RenderPool(spec PoolSpec) ([]byte, error) {
	md := BuildMachineDeployment(spec)
	md.TypeMeta = metav1.TypeMeta{APIVersion: clusterAPIVersion, Kind: machineDeploymentKind}
	out, err := yaml.Marshal(md)
	if err != nil {
		return nil, fmt.Errorf("capi: marshal pool %q: %w", spec.Name, err)
	}
	return out, nil
}

func buildCluster(spec ClusterSpec) *clusterv1.Cluster {
	cluster := &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{APIVersion: clusterAPIVersion, Kind: clusterKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.ClusterName,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				managedByLabel: managedByValue,
			},
		},
		Spec: clusterv1.ClusterSpec{
			ClusterNetwork: clusterv1.ClusterNetwork{
				Pods:     clusterv1.NetworkRanges{CIDRBlocks: []string{spec.PodCIDR}},
				Services: clusterv1.NetworkRanges{CIDRBlocks: []string{spec.ServiceCIDR}},
			},
			InfrastructureRef: spec.ClusterInfrastructure.objectReference(),
		},
	}
	if spec.ControlPlaneMode == Managed {
		cluster.Spec.ControlPlaneRef = clusterv1.ContractVersionedObjectReference{
			APIGroup: kThreesControlPlaneGroup,
			Kind:     kThreesControlPlaneKind,
			Name:     spec.Name,
		}
	}
	return cluster
}

func buildControlPlane(spec ClusterSpec) *unstructured.Unstructured {
	cp := &unstructured.Unstructured{}
	cp.SetAPIVersion(kThreesControlPlaneAPI)
	cp.SetKind(kThreesControlPlaneKind)
	cp.SetName(spec.Name)
	cp.SetNamespace(spec.Namespace)
	cp.SetLabels(map[string]string{managedByLabel: managedByValue})
	cp.Object["spec"] = map[string]any{
		"replicas": int64(spec.Replicas),
		"version":  spec.Version,
		"machineTemplate": map[string]any{
			"infrastructureRef": map[string]any{
				"apiGroup": spec.ControlPlaneInfra.APIGroup,
				"kind":     spec.ControlPlaneInfra.Kind,
				"name":     spec.ControlPlaneInfra.Name,
			},
		},
	}
	return cp
}

func workerPoolSpec(spec ClusterSpec) PoolSpec {
	return PoolSpec{
		Name:           spec.Name + "-workers",
		Namespace:      spec.Namespace,
		ClusterName:    spec.ClusterName,
		Replicas:       spec.Replicas,
		Version:        spec.Version,
		Infrastructure: spec.Infrastructure,
		Bootstrap:      spec.Bootstrap,
	}
}

func RenderCluster(spec ClusterSpec) ([]byte, error) {
	clusterYAML, err := yaml.Marshal(buildCluster(spec))
	if err != nil {
		return nil, fmt.Errorf("capi: marshal cluster %q: %w", spec.ClusterName, err)
	}
	docs := [][]byte{clusterYAML}

	if spec.ControlPlaneMode == Managed {
		cpYAML, err := yaml.Marshal(buildControlPlane(spec))
		if err != nil {
			return nil, fmt.Errorf("capi: marshal control plane %q: %w", spec.Name, err)
		}
		docs = append(docs, cpYAML)
	}

	workerYAML, err := RenderPool(workerPoolSpec(spec))
	if err != nil {
		return nil, err
	}
	docs = append(docs, workerYAML)

	return bytes.Join(docs, []byte("---\n")), nil
}
