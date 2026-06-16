package capi

import (
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/yaml"
)

const (
	clusterAPIVersion     = "cluster.x-k8s.io/v1beta2"
	machineDeploymentKind = "MachineDeployment"
	clusterKind           = "Cluster"
)

type ClusterVariable struct {
	Name  string
	Value string
}

type ClusterSpec struct {
	Name                 string
	Namespace            string
	Class                string
	WorkerClass          string
	WorkerName           string
	Version              string
	ControlPlaneReplicas int32
	WorkerReplicas       int32
	Variables            []ClusterVariable
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

func encodeVariableValue(raw string) (apiextensionsv1.JSON, error) {
	if json.Valid([]byte(raw)) {
		return apiextensionsv1.JSON{Raw: []byte(raw)}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return apiextensionsv1.JSON{}, err
	}
	return apiextensionsv1.JSON{Raw: encoded}, nil
}

func buildTopologyCluster(spec ClusterSpec) (*clusterv1.Cluster, error) {
	controlPlaneReplicas := spec.ControlPlaneReplicas
	topology := clusterv1.Topology{
		ClassRef: clusterv1.ClusterClassRef{Name: spec.Class},
		Version:  spec.Version,
		ControlPlane: clusterv1.ControlPlaneTopology{
			Replicas: &controlPlaneReplicas,
		},
	}

	if spec.WorkerReplicas > 0 {
		workerReplicas := spec.WorkerReplicas
		topology.Workers.MachineDeployments = []clusterv1.MachineDeploymentTopology{{
			Class:    spec.WorkerClass,
			Name:     spec.WorkerName,
			Replicas: &workerReplicas,
		}}
	}

	for _, v := range spec.Variables {
		value, err := encodeVariableValue(v.Value)
		if err != nil {
			return nil, fmt.Errorf("capi: encode variable %q: %w", v.Name, err)
		}
		topology.Variables = append(topology.Variables, clusterv1.ClusterVariable{
			Name:  v.Name,
			Value: value,
		})
	}

	return &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{APIVersion: clusterAPIVersion, Kind: clusterKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				managedByLabel: managedByValue,
			},
		},
		Spec: clusterv1.ClusterSpec{
			Topology: topology,
		},
	}, nil
}

func RenderCluster(spec ClusterSpec) ([]byte, error) {
	cluster, err := buildTopologyCluster(spec)
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("capi: marshal cluster %q: %w", spec.Name, err)
	}
	return out, nil
}
