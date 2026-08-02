package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
)

func (r *CapacityLeaseReconciler) reconcileNodes(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	nodes, err := r.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("list nodes: %w", err)
	}

	changed := false
	joined := 0
	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		if entry.Phase != v1alpha1.InstancePhaseCreated && entry.Phase != v1alpha1.InstancePhaseJoined {
			continue
		}
		node := matchNode(nodes.Items, entry)
		if node == nil {
			continue
		}
		if entry.NodeName != node.Name {
			entry.NodeName = node.Name
			changed = true
		}
		if err := r.adoptNode(ctx, lease, node); err != nil {
			return ctrl.Result{}, err
		}
		if !nodeReady(node) {
			continue
		}
		if entry.Phase != v1alpha1.InstancePhaseJoined {
			entry.Phase = v1alpha1.InstancePhaseJoined
			changed = true
		}
		joined++
	}

	want := int(lease.Spec.Replicas)
	if joined >= want {
		changed = setCondition(lease, v1alpha1.ConditionInstancesReady, metav1.ConditionTrue, reasonNodesReady,
			fmt.Sprintf("%d of %d nodes ready", joined, want)) || changed
	} else {
		changed = setCondition(lease, v1alpha1.ConditionInstancesReady, metav1.ConditionFalse, reasonWaitingForNodes,
			fmt.Sprintf("%d of %d nodes ready", joined, want)) || changed
	}

	if changed {
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, err
		}
	}
	if joined < want {
		return r.nextPoll(lease), nil
	}
	return ctrl.Result{}, nil
}

func matchNode(nodes []corev1.Node, entry *v1alpha1.InstanceStatus) *corev1.Node {
	if entry.ProviderID != "" {
		for i := range nodes {
			if nodes[i].Spec.ProviderID == entry.ProviderID {
				return &nodes[i]
			}
		}
	}
	for i := range nodes {
		if nodes[i].Name == entry.Name {
			return &nodes[i]
		}
	}
	return nil
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (r *CapacityLeaseReconciler) adoptNode(ctx context.Context, lease *v1alpha1.CapacityLease, node *corev1.Node) error {
	updated := node.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = map[string]string{}
	}
	for key, value := range nodeLabels(lease) {
		updated.Labels[key] = value
	}
	if !hasBurstTaint(updated) {
		updated.Spec.Taints = append(updated.Spec.Taints, burstTaint(lease))
	}
	if equalNodeMarks(node, updated) {
		return nil
	}
	if _, err := r.Kube.CoreV1().Nodes().Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("label node %q for lease %q: %w", node.Name, lease.Name, err)
	}
	return nil
}

func nodeLabels(lease *v1alpha1.CapacityLease) map[string]string {
	return map[string]string{
		provider.PoolLabelKey: provider.ReservedPoolValue,
		LeaseNameLabelKey:     lease.Name,
		LeaseUIDLabelKey:      string(lease.UID),
	}
}

func burstTaint(lease *v1alpha1.CapacityLease) corev1.Taint {
	return corev1.Taint{Key: k8s.BurstTaintKey, Value: lease.Name, Effect: corev1.TaintEffectNoSchedule}
}

func hasBurstTaint(node *corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == k8s.BurstTaintKey && taint.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func equalNodeMarks(current, updated *corev1.Node) bool {
	if len(current.Spec.Taints) != len(updated.Spec.Taints) {
		return false
	}
	for key, value := range updated.Labels {
		if current.Labels[key] != value {
			return false
		}
	}
	return true
}

func (r *CapacityLeaseReconciler) deleteOwnedNode(ctx context.Context, lease *v1alpha1.CapacityLease, nodeName string) error {
	if nodeName == "" {
		return nil
	}
	node, err := r.Kube.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get node %q: %w", nodeName, err)
	}
	if node.Labels[LeaseUIDLabelKey] != string(lease.UID) {
		return fmt.Errorf("refusing to delete node %q: it does not carry %s=%s: %w",
			nodeName, LeaseUIDLabelKey, lease.UID, errReleaseDegraded)
	}
	opts := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &node.UID}}
	if err := r.Kube.CoreV1().Nodes().Delete(ctx, nodeName, opts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete node %q: %w", nodeName, err)
	}
	return nil
}
