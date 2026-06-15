package capi

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	poolNameLabel    = "horizon.dev/pool-name"
	poolTypeLabel    = "horizon.dev/pool-type"
	managedByLabel   = "horizon.dev/managed-by"
	managedByValue   = "horizon"
	clusterNameLabel = clusterv1.ClusterNameLabel
)

type TemplateRef struct {
	APIGroup string
	Kind     string
	Name     string
}

type PoolSpec struct {
	Name           string
	Type           string
	Namespace      string
	ClusterName    string
	Replicas       int32
	Version        string
	Infrastructure TemplateRef
	Bootstrap      TemplateRef
}

func PoolType(md *clusterv1.MachineDeployment) string {
	if md == nil {
		return ""
	}
	return md.Labels[poolTypeLabel]
}

func (r TemplateRef) objectReference() clusterv1.ContractVersionedObjectReference {
	return clusterv1.ContractVersionedObjectReference{
		APIGroup: r.APIGroup,
		Kind:     r.Kind,
		Name:     r.Name,
	}
}

func BuildMachineDeployment(spec PoolSpec) *clusterv1.MachineDeployment {
	labels := map[string]string{
		poolNameLabel:    spec.Name,
		managedByLabel:   managedByValue,
		clusterNameLabel: spec.ClusterName,
	}
	if spec.Type != "" {
		labels[poolTypeLabel] = spec.Type
	}
	replicas := spec.Replicas
	version := spec.Version
	bootstrapRef := spec.Bootstrap.objectReference()

	return &clusterv1.MachineDeployment{
		// omitting the autoscaler min/max-size annotations keeps manual pools out of the cluster-autoscaler's control
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    labels,
		},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: spec.ClusterName,
			Replicas:    &replicas,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					managedByLabel:   managedByValue,
					poolNameLabel:    spec.Name,
					clusterNameLabel: spec.ClusterName,
				},
			},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels: map[string]string{
						managedByLabel:   managedByValue,
						poolNameLabel:    spec.Name,
						clusterNameLabel: spec.ClusterName,
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: spec.ClusterName,
					Version:     version,
					Bootstrap: clusterv1.Bootstrap{
						ConfigRef: bootstrapRef,
					},
					InfrastructureRef: spec.Infrastructure.objectReference(),
				},
			},
		},
	}
}

func (c *Client) GetPool(ctx context.Context, namespace, name string) (*clusterv1.MachineDeployment, error) {
	md := &clusterv1.MachineDeployment{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.crClient().Get(ctx, key, md); err != nil {
		return nil, err
	}
	return md, nil
}

func (c *Client) ListPools(ctx context.Context, namespace string) ([]clusterv1.MachineDeployment, error) {
	return c.ListPoolsForCluster(ctx, namespace, "")
}

func (c *Client) ListPoolsForCluster(ctx context.Context, namespace, clusterName string) ([]clusterv1.MachineDeployment, error) {
	labels := client.MatchingLabels{managedByLabel: managedByValue}
	if clusterName != "" {
		labels[clusterNameLabel] = clusterName
	}
	list := &clusterv1.MachineDeploymentList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		labels,
	}
	if err := c.crClient().List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("capi: list pools in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (c *Client) ApplyPool(ctx context.Context, spec PoolSpec) (*clusterv1.MachineDeployment, error) {
	existing, err := c.GetPool(ctx, spec.Namespace, spec.Name)
	if apierrors.IsNotFound(err) {
		md := BuildMachineDeployment(spec)
		if err := c.crClient().Create(ctx, md); err != nil {
			return nil, fmt.Errorf("capi: create pool %q: %w", spec.Name, err)
		}
		return md, nil
	}
	if err != nil {
		return nil, fmt.Errorf("capi: get pool %q: %w", spec.Name, err)
	}

	desired := BuildMachineDeployment(spec)
	existing.Spec.Replicas = desired.Spec.Replicas
	existing.Spec.Template = desired.Spec.Template
	if err := c.crClient().Update(ctx, existing); err != nil {
		return nil, fmt.Errorf("capi: update pool %q: %w", spec.Name, err)
	}
	return existing, nil
}

func (c *Client) ScalePool(ctx context.Context, namespace, name string, replicas int32) error {
	md, err := c.GetPool(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("capi: get pool %q: %w", name, err)
	}
	patch := client.MergeFrom(md.DeepCopy())
	md.Spec.Replicas = &replicas
	if err := c.crClient().Patch(ctx, md, patch); err != nil {
		return fmt.Errorf("capi: scale pool %q: %w", name, err)
	}
	return nil
}

func (c *Client) DeletePool(ctx context.Context, namespace, name string) error {
	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if err := c.crClient().Delete(ctx, md); err != nil {
		return fmt.Errorf("capi: delete pool %q: %w", name, err)
	}
	return nil
}

func (c *Client) ListMachines(ctx context.Context, namespace, poolName string) ([]clusterv1.Machine, error) {
	list := &clusterv1.MachineList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{clusterv1.MachineDeploymentNameLabel: poolName},
	}
	if err := c.crClient().List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("capi: list machines for pool %q: %w", poolName, err)
	}
	return list.Items, nil
}
