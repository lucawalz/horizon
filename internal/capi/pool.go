package capi

import (
	"context"
	"fmt"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const poolTypeLabel = "horizon.dev/pool-type"

func PoolType(md *clusterv1.MachineDeployment) string {
	if md == nil {
		return ""
	}
	return md.Labels[poolTypeLabel]
}

func (c *Client) ListMachineDeploymentsByType(ctx context.Context, namespace string) ([]clusterv1.MachineDeployment, error) {
	list := &clusterv1.MachineDeploymentList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.HasLabels{poolTypeLabel},
	}
	if err := c.crClient().List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("capi: list pools by type in %q: %w", namespace, err)
	}
	return list.Items, nil
}
