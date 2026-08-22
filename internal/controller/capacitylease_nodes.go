package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

func (r *CapacityLeaseReconciler) reconcileNodes(ctx context.Context, lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) (ctrl.Result, error) {
	nodes, err := r.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("list nodes: %w", err)
	}

	renewal := newWatchdogRenewal(lease, policy, r.now())
	var records metricWrites
	changed := false
	renewed := false
	joined := 0
	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		if entry.Phase != v1alpha1.InstancePhaseCreated && entry.Phase != v1alpha1.InstancePhaseJoined {
			changed = recordStage(entry, instanceStage(entry, nil)) || changed
			continue
		}
		node := matchNode(nodes.Items, entry)
		changed = recordStage(entry, instanceStage(entry, node)) || changed
		if node == nil {
			continue
		}
		if entry.NodeName != node.Name {
			entry.NodeName = node.Name
			changed = true
		}
		fresh, err := r.adoptNode(ctx, lease, node, renewal)
		if err != nil {
			return ctrl.Result{}, err
		}
		renewed = renewed || fresh
		if !nodeReady(node) {
			continue
		}
		if entry.Phase != v1alpha1.InstancePhaseJoined {
			entry.Phase = v1alpha1.InstancePhaseJoined
			changed = true
		}
		joined++
	}

	if renewed {
		changed = recordWatchdogDeadline(lease, renewal.deadline) || changed
	}

	changed = r.reconcileWatchdogArmed(lease, nodes.Items, policy) || changed

	want := int(lease.Spec.Replicas)
	if joined >= want {
		changed = setCondition(lease, v1alpha1.ConditionInstancesReady, metav1.ConditionTrue, reasonNodesReady,
			fmt.Sprintf("%d of %d nodes ready", joined, want)) || changed
		if lease.Status.ReadyAt == nil {
			ready := r.now()
			lease.Status.ReadyAt = &metav1.Time{Time: ready}
			changed = true
			records.add(readyRecord(attributionOf(lease), selectionOf(lease), ready.Sub(lease.Status.AcceptedAt.Time)))
		}
	} else {
		reason, message := waitingCondition(lease, joined, want, r.now())
		changed = setCondition(lease, v1alpha1.ConditionInstancesReady, metav1.ConditionFalse, reason, message) || changed
	}

	if changed {
		if err := r.writeStatus(ctx, lease, records...); err != nil {
			return ctrl.Result{}, err
		}
	}
	if joined < want {
		return r.nextPoll(lease, policy), nil
	}
	return ctrl.Result{}, nil
}

type waitingStage struct {
	rank   int
	reason string
	format string
}

var waitingStages = map[v1alpha1.InstanceStage]waitingStage{
	v1alpha1.InstanceStageAwaitingInstance: {
		rank:   0,
		reason: reasonAwaitingInstance,
		format: "instance %s was requested %s ago and no provider instance exists yet",
	},
	v1alpha1.InstanceStageAwaitingRegistration: {
		rank:   1,
		reason: reasonAwaitingRegistration,
		format: "instance %s was created %s ago and no node has registered",
	},
	v1alpha1.InstanceStageAwaitingReady: {
		rank:   2,
		reason: reasonAwaitingReady,
		format: "instance %s was created %s ago and its node is not ready",
	},
}

// a draining or released entry has stopped working towards readiness, so it names no waiting stage
func instanceStage(entry *v1alpha1.InstanceStatus, node *corev1.Node) v1alpha1.InstanceStage {
	switch {
	case entry.Phase == v1alpha1.InstancePhaseIntended:
		return v1alpha1.InstanceStageAwaitingInstance
	case entry.Phase != v1alpha1.InstancePhaseCreated && entry.Phase != v1alpha1.InstancePhaseJoined:
		return ""
	case node == nil:
		return v1alpha1.InstanceStageAwaitingRegistration
	case !nodeReady(node):
		return v1alpha1.InstanceStageAwaitingReady
	default:
		return v1alpha1.InstanceStageReady
	}
}

func recordStage(entry *v1alpha1.InstanceStatus, stage v1alpha1.InstanceStage) bool {
	if entry.Stage == stage {
		return false
	}
	entry.Stage = stage
	return true
}

func waitingCondition(lease *v1alpha1.CapacityLease, joined, want int, now time.Time) (reason, message string) {
	count := fmt.Sprintf("%d of %d nodes ready", joined, want)
	blocking := leastAdvancedInstance(lease)
	if blocking == nil {
		return reasonWaitingForNodes, count
	}
	stage := waitingStages[blocking.Stage]
	detail := fmt.Sprintf(stage.format, blocking.Name, elapsedSince(blocking.CreatedAt, now))
	return stage.reason, count + "; " + detail
}

func leastAdvancedInstance(lease *v1alpha1.CapacityLease) *v1alpha1.InstanceStatus {
	var blocking *v1alpha1.InstanceStatus
	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		stage, waiting := waitingStages[entry.Stage]
		if !waiting {
			continue
		}
		if blocking == nil || stage.rank < waitingStages[blocking.Stage].rank {
			blocking = entry
		}
	}
	return blocking
}

func elapsedSince(instant *metav1.Time, now time.Time) string {
	if instant == nil {
		return "an unknown time"
	}
	return duration.HumanDuration(now.Sub(instant.Time))
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

func (r *CapacityLeaseReconciler) adoptNode(ctx context.Context, lease *v1alpha1.CapacityLease, node *corev1.Node, renewal watchdogRenewal) (bool, error) {
	annotations, renewed := renewal.annotationsFor(node)
	err := r.patchNodeMarks(ctx, lease, node, annotations)
	if renewed {
		metrics.RecordWatchdogRenewal(lease.Status.ProviderConfig, renewalResult(err))
	}
	if err != nil {
		return false, err
	}
	if err := r.ensureBurstTaint(ctx, lease, node); err != nil {
		return false, err
	}
	return renewed, nil
}

func (r *CapacityLeaseReconciler) patchNodeMarks(ctx context.Context, lease *v1alpha1.CapacityLease, node *corev1.Node, annotations map[string]string) error {
	labels := nodeLabels(lease)
	if containsAll(node.Labels, labels) && containsAll(node.Annotations, annotations) {
		return nil
	}
	patch, err := json.Marshal(map[string]map[string]map[string]string{
		"metadata": {"labels": labels, "annotations": annotations},
	})
	if err != nil {
		return fmt.Errorf("build marks patch for node %q: %w", node.Name, err)
	}
	if _, err := r.Kube.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("mark node %q for lease %q: %w", node.Name, lease.Name, err)
	}
	return nil
}

func (r *CapacityLeaseReconciler) ensureBurstTaint(ctx context.Context, lease *v1alpha1.CapacityLease, node *corev1.Node) error {
	if hasBurstTaint(node) {
		return nil
	}
	nodes := r.Kube.CoreV1().Nodes()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := nodes.Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if hasBurstTaint(current) {
			return nil
		}
		current.Spec.Taints = append(current.Spec.Taints, burstTaint(lease))
		_, err = nodes.Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("taint node %q for lease %q: %w", node.Name, lease.Name, err)
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

func containsAll(current, desired map[string]string) bool {
	for key, value := range desired {
		if current[key] != value {
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
