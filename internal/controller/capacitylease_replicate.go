package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
)

func (r *CapacityLeaseReconciler) replicateWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	targets, err := k8s.NewTargetSet(lease.Spec.Workload.Namespaces, lease.Spec.Workload.Selector)
	if err != nil {
		return r.failReplication(ctx, lease, reasonReplicateFailed, fmt.Errorf("read the workload target set: %w", err))
	}

	result, replicateErr := k8s.Replicate(ctx, r.Kube, targets, replicationOf(lease))
	// only a successful teardown may shrink this list, because it is the sole record of the copies teardown still has to delete
	lease.Status.MigratedWorkloads = alsoMigrated(lease.Status.MigratedWorkloads, result.Copies)
	lease.Status.MigrationWarnings = alsoWarned(lease.Status.MigrationWarnings, result)
	if replicateErr != nil {
		reason, cause := replicationOutcome(result, len(lease.Spec.Workload.Namespaces), replicateErr)
		return r.failReplication(ctx, lease, reason, cause)
	}
	if result.Matched() == 0 {
		return r.failReplication(ctx, lease, reasonNoMatchingWorkloads, fmt.Errorf(
			"the workload target set names no deployment or statefulset in %s",
			strings.Join(lease.Spec.Workload.Namespaces, ", "),
		))
	}
	if len(result.Copies) == 0 {
		return r.failReplication(ctx, lease, reasonEveryWorkloadSkipped, errors.New(skipSummary(result.Skipped)))
	}

	r.setCondition(lease, v1alpha1.ConditionWorkloadReplicable, metav1.ConditionTrue, reasonReplicated,
		replicationSummary(result))
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func (r *CapacityLeaseReconciler) failReplication(ctx context.Context, lease *v1alpha1.CapacityLease, reason string, cause error) (ctrl.Result, error) {
	return r.failWorkload(ctx, lease, v1alpha1.ConditionWorkloadReplicable, reason, cause)
}

func replicationOf(lease *v1alpha1.CapacityLease) k8s.Replication {
	return k8s.Replication{
		Lease:    leaseIdentity(lease),
		Replicas: burstReplicas(lease),
		Owner:    leaseOwnerReference(lease),
	}
}

func burstReplicas(lease *v1alpha1.CapacityLease) int32 {
	if lease.Spec.Workload == nil || lease.Spec.Workload.BurstReplicas == nil {
		return 0
	}
	return *lease.Spec.Workload.BurstReplicas
}

func leaseOwnerReference(lease *v1alpha1.CapacityLease) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion: v1alpha1.GroupVersion.String(),
		Kind:       capacityLeaseKind,
		Name:       lease.Name,
		UID:        lease.UID,
		Controller: &controller,
	}
}

func replicationWarnings(result k8s.ReplicationResult) []v1alpha1.MigrationWarning {
	var warnings []v1alpha1.MigrationWarning
	for _, flagged := range slices.Concat(result.Skipped, result.Warnings) {
		warnings = append(warnings, v1alpha1.MigrationWarning{
			Workload: flagged.Workload,
			Reasons:  flagged.Reasons,
		})
	}
	return warnings
}

func alsoWarned(recorded []v1alpha1.MigrationWarning, result k8s.ReplicationResult) []v1alpha1.MigrationWarning {
	warnings := replicationWarnings(result)
	named := map[string]bool{}
	for _, warning := range warnings {
		named[warning.Workload] = true
	}
	for _, warning := range recorded {
		// a namespace this pass never read through keeps what the pass that did read it recorded, because its copies are still running
		if named[warning.Workload] || slices.Contains(result.ReplicatedNamespaces, k8s.WorkloadNamespace(warning.Workload)) {
			continue
		}
		warnings = append(warnings, warning)
	}
	return warnings
}

func replicationOutcome(result k8s.ReplicationResult, targeted int, cause error) (string, error) {
	if len(result.ReplicatedNamespaces) == 0 {
		return reasonReplicateFailed, fmt.Errorf("replicate workload: %w", cause)
	}
	return reasonPartialReplication, fmt.Errorf("replicated %d of %d namespaces, leaving %d copies running: %w",
		len(result.ReplicatedNamespaces), targeted, len(result.Copies), cause)
}

func replicationSummary(result k8s.ReplicationResult) string {
	summary := fmt.Sprintf("%d workloads replicated onto burst capacity", len(result.Copies))
	if len(result.Skipped) == 0 {
		return summary
	}
	return summary + "; " + skipSummary(result.Skipped)
}

func skipSummary(skipped []k8s.WorkloadWarning) string {
	sentences := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		for _, reason := range skip.Reasons {
			sentences = append(sentences, fmt.Sprintf("%s was not replicated because %s",
				skip.Workload, k8s.ReplicationReasonText(reason)))
		}
	}
	return strings.Join(sentences, "; ")
}
