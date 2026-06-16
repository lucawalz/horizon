package capi

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (c *Client) ListClusters(ctx context.Context, namespace string) ([]clusterv1.Cluster, error) {
	list := &clusterv1.ClusterList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{managedByLabel: managedByValue},
	}
	if err := c.crClient().List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("capi: list clusters in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (c *Client) DeleteCluster(ctx context.Context, namespace, name string) error {
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if err := c.crClient().Delete(ctx, cluster); err != nil {
		return fmt.Errorf("capi: delete cluster %q: %w", name, err)
	}
	return nil
}

func (c *Client) ApplyCluster(ctx context.Context, spec ClusterSpec) error {
	desired, err := buildTopologyCluster(spec)
	if err != nil {
		return err
	}
	existing, err := c.GetCluster(ctx, spec.Namespace, spec.Name)
	if apierrors.IsNotFound(err) {
		if err := c.crClient().Create(ctx, desired); err != nil {
			return fmt.Errorf("capi: create cluster %q: %w", spec.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("capi: get cluster %q: %w", spec.Name, err)
	}
	existing.Spec = desired.Spec
	if err := c.crClient().Update(ctx, existing); err != nil {
		return fmt.Errorf("capi: update cluster %q: %w", spec.Name, err)
	}
	return nil
}
