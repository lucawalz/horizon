package capi

import (
	"context"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *Client) GetCluster(ctx context.Context, namespace, name string) (*clusterv1.Cluster, error) {
	cluster := &clusterv1.Cluster{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.crClient().Get(ctx, key, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}
