package capi

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	clusterAPIVersion     = "cluster.x-k8s.io/v1beta2"
	machineDeploymentKind = "MachineDeployment"
)

func RenderPool(spec PoolSpec) ([]byte, error) {
	md := BuildMachineDeployment(spec)
	md.TypeMeta = metav1.TypeMeta{APIVersion: clusterAPIVersion, Kind: machineDeploymentKind}
	out, err := yaml.Marshal(md)
	if err != nil {
		return nil, fmt.Errorf("capi: marshal pool %q: %w", spec.Name, err)
	}
	return out, nil
}
