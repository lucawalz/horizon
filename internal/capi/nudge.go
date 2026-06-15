package capi

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *Client) IsControlPlaneInitialized(ctx context.Context, namespace, clusterName string) (bool, error) {
	cluster, err := c.GetCluster(ctx, namespace, clusterName)
	if err != nil {
		return false, err
	}
	init := cluster.Status.Initialization.ControlPlaneInitialized
	return init != nil && *init, nil
}

func (c *Client) NudgeControlPlaneInitialized(ctx context.Context, namespace, clusterName string) error {
	cluster, err := c.GetCluster(ctx, namespace, clusterName)
	if err != nil {
		return err
	}
	orig := cluster.DeepCopy()
	initialized := true
	cluster.Status.Initialization.ControlPlaneInitialized = &initialized
	if err := c.crClient().Status().Patch(ctx, cluster, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("capi: nudge control-plane-initialized for %q: %w", clusterName, err)
	}
	return nil
}
