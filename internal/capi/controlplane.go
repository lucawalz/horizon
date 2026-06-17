package capi

import (
	"context"
)

func (c *Client) IsControlPlaneInitialized(ctx context.Context, namespace, clusterName string) (bool, error) {
	cluster, err := c.GetCluster(ctx, namespace, clusterName)
	if err != nil {
		return false, err
	}
	init := cluster.Status.Initialization.ControlPlaneInitialized
	return init != nil && *init, nil
}
