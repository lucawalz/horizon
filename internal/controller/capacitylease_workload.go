package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
)

func (r *CapacityLeaseReconciler) reconcileWorkload(ctx context.Context, lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) (ctrl.Result, error) {
	if lease.Spec.Workload == nil {
		return ctrl.Result{}, nil
	}
	if !conditionTrue(lease, v1alpha1.ConditionInstancesReady) {
		return r.nextPoll(lease, policy), nil
	}

	if !conditionTrue(lease, workloadPlacedCondition(lease)) {
		return r.placeWorkload(ctx, lease)
	}

	namespaces, err := everyWorkloadInNamespaces(lease)
	if err != nil {
		return ctrl.Result{}, err
	}
	placed, err := k8s.WorkloadOnBurstNodes(ctx, r.Kube, namespaces, leaseIdentity(lease))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check workload placement: %w", err)
	}
	if !placed {
		return r.nextPoll(lease, policy), nil
	}
	return ctrl.Result{}, nil
}

func placedMode(lease *v1alpha1.CapacityLease) v1alpha1.WorkloadMode {
	if lease.Status.PlacedWorkloadMode != "" {
		return lease.Status.PlacedWorkloadMode
	}
	if lease.Spec.Workload == nil {
		return ""
	}
	return lease.Spec.Workload.Mode
}

func replicateMode(lease *v1alpha1.CapacityLease) bool {
	return placedMode(lease) == v1alpha1.WorkloadModeReplicate
}

func workloadPlacedCondition(lease *v1alpha1.CapacityLease) string {
	if replicateMode(lease) {
		return v1alpha1.ConditionWorkloadReplicable
	}
	return v1alpha1.ConditionWorkloadMigrated
}

func (r *CapacityLeaseReconciler) placeWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	// teardown undoes what the lease did, so the mode is latched before the first write and never read back from a spec that can still change
	if lease.Status.PlacedWorkloadMode == "" {
		lease.Status.PlacedWorkloadMode = lease.Spec.Workload.Mode
	}
	if replicateMode(lease) {
		return r.replicateWorkload(ctx, lease)
	}
	return r.migrateWorkload(ctx, lease)
}

func everyWorkloadInNamespaces(lease *v1alpha1.CapacityLease) (k8s.TargetSet, error) {
	namespaces, err := k8s.NewNamespaceSet(lease.Spec.Workload.Namespaces)
	if err != nil {
		return k8s.TargetSet{}, fmt.Errorf("read the workload target set of lease %q: %w", lease.Name, err)
	}
	return namespaces, nil
}

func migratedNamespaces(lease *v1alpha1.CapacityLease) (k8s.TargetSet, error) {
	namespaces, err := k8s.NamespaceSetOfWorkloads(lease.Status.MigratedWorkloads)
	if err != nil {
		return k8s.TargetSet{}, fmt.Errorf("read the migrated workloads of lease %q: %w", lease.Name, err)
	}
	return namespaces, nil
}

func (r *CapacityLeaseReconciler) migrateWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	targets, err := k8s.NewTargetSet(lease.Spec.Workload.Namespaces, lease.Spec.Workload.Selector)
	if err != nil {
		return r.failMigration(ctx, lease, reasonMigrateFailed, fmt.Errorf("read the workload target set: %w", err))
	}

	assessments, classifyErr := k8s.ClassifyMigratability(ctx, r.Kube, targets, leaseIdentity(lease))
	r.recordMigratability(lease, assessments, classifyErr)

	result, migrateErr := k8s.Migrate(ctx, r.Kube, targets, leaseIdentity(lease), teardownGrace(lease))
	// only a successful restore may shrink this list, because it is the sole record of what teardown still has to put back
	lease.Status.MigratedWorkloads = alsoMigrated(lease.Status.MigratedWorkloads, result.Workloads)
	if migrateErr != nil {
		reason, cause := migrationOutcome(result, len(lease.Spec.Workload.Namespaces), migrateErr)
		return r.failMigration(ctx, lease, reason, cause)
	}
	if len(result.Workloads) == 0 {
		return r.failMigration(ctx, lease, reasonEmptyTargetSet,
			errors.New("the workload target set names no deployment or statefulset to move"))
	}

	r.setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue, reasonMigrated,
		fmt.Sprintf("%d workloads moved onto burst capacity", len(lease.Status.MigratedWorkloads)))
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func alsoMigrated(recorded, moved []string) []string {
	union := slices.Clone(recorded)
	for _, ref := range moved {
		if !slices.Contains(union, ref) {
			union = append(union, ref)
		}
	}
	return union
}

func migrationOutcome(result k8s.MigrationResult, targeted int, cause error) (string, error) {
	if len(result.MigratedNamespaces) == 0 {
		return reasonMigrateFailed, fmt.Errorf("migrate workload: %w", cause)
	}
	return reasonPartialMigration, fmt.Errorf("migrated %d of %d namespaces, leaving %d workloads moved: %w",
		len(result.MigratedNamespaces), targeted, len(result.Workloads), cause)
}

func (r *CapacityLeaseReconciler) failMigration(ctx context.Context, lease *v1alpha1.CapacityLease, reason string, cause error) (ctrl.Result, error) {
	return r.failWorkload(ctx, lease, v1alpha1.ConditionWorkloadMigrated, reason, cause)
}

func (r *CapacityLeaseReconciler) failWorkload(ctx context.Context, lease *v1alpha1.CapacityLease, condition, reason string, cause error) (ctrl.Result, error) {
	r.setCondition(lease, condition, metav1.ConditionFalse, reason, cause.Error())
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, errors.Join(cause, err)
	}
	return ctrl.Result{}, cause
}

func (r *CapacityLeaseReconciler) recordMigratability(lease *v1alpha1.CapacityLease, assessments []k8s.WorkloadMigratability, classifyErr error) {
	if classifyErr != nil {
		// unknown is not the same fact as nothing to warn about, and neither is a reason to leave the workload where it is
		lease.Status.MigrationWarnings = nil
		r.setCondition(lease, v1alpha1.ConditionWorkloadMigratable, metav1.ConditionUnknown, reasonClassificationFailed, classifyErr.Error())
		return
	}

	var warnings []v1alpha1.MigrationWarning
	for _, assessment := range assessments {
		if assessment.Verdict == k8s.VerdictSeamless {
			continue
		}
		warnings = append(warnings, v1alpha1.MigrationWarning{
			Workload: assessment.Workload,
			Reasons:  assessment.Reasons,
		})
	}

	lease.Status.MigrationWarnings = warnings
	if len(warnings) == 0 {
		r.setCondition(lease, v1alpha1.ConditionWorkloadMigratable, metav1.ConditionTrue, reasonSeamlessMigration,
			fmt.Sprintf("%d workloads move without dropping traffic", len(assessments)))
		return
	}
	r.setCondition(lease, v1alpha1.ConditionWorkloadMigratable, metav1.ConditionFalse, reasonDisruptiveMigration,
		migratabilitySummary(warnings, len(assessments)))
}

func migratabilitySummary(warnings []v1alpha1.MigrationWarning, total int) string {
	held := 0
	for _, warning := range warnings {
		if slices.Contains(warning.Reasons, k8s.ReasonHeldByAnotherLease) {
			held++
		}
	}
	var parts []string
	if moving := len(warnings) - held; moving > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d workloads lose availability while moving onto burst capacity", moving, total))
	}
	if held > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d workloads stay where they are, held by another lease", held, total))
	}
	return strings.Join(parts, "; ")
}

func (r *CapacityLeaseReconciler) restoreWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	// what the lease placed decides both whether to act and where, because a partial placement leaves the condition False and the spec is mutable
	if len(lease.Status.MigratedWorkloads) == 0 {
		return ctrl.Result{}, nil
	}

	if reason, releaseErr := r.releaseBurstWorkload(ctx, lease); releaseErr != nil {
		r.setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, reason, releaseErr.Error())
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, errors.Join(releaseErr, err)
		}
		// blocking past this point would strand the machines and the finalizer as well as the workload, so the drain share ends the wait
		if r.remainingTeardownBudget(lease) > reservedDrainBudget(lease) {
			return ctrl.Result{}, releaseErr
		}
		return ctrl.Result{}, nil
	}

	lease.Status.MigratedWorkloads = nil
	lease.Status.MigrationWarnings = nil
	r.markWorkloadReleased(lease)
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func (r *CapacityLeaseReconciler) releaseBurstWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (string, error) {
	if replicateMode(lease) {
		if err := k8s.DeleteBurstCopies(ctx, r.Kube, lease.Status.MigratedWorkloads, leaseIdentity(lease)); err != nil {
			return reasonCopyDeleteFailed, fmt.Errorf("delete burst copies: %w", err)
		}
		return "", nil
	}
	namespaces, err := migratedNamespaces(lease)
	if err != nil {
		return reasonRestoreFailed, err
	}
	if _, err := k8s.RestorePlacement(ctx, r.Kube, namespaces, leaseIdentity(lease)); err != nil {
		return reasonRestoreFailed, fmt.Errorf("restore placement: %w", err)
	}
	return "", nil
}

func (r *CapacityLeaseReconciler) markWorkloadReleased(lease *v1alpha1.CapacityLease) {
	if replicateMode(lease) {
		r.setCondition(lease, v1alpha1.ConditionWorkloadReplicable, metav1.ConditionFalse, reasonCopiesDeleted,
			"burst copies deleted")
		return
	}
	meta.RemoveStatusCondition(&lease.Status.Conditions, v1alpha1.ConditionWorkloadMigratable)
	r.setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse, reasonPlacementRestored,
		"workload placement restored")
}

func (r *CapacityLeaseReconciler) awaitWorkloadRestored(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Spec.Workload == nil {
		return ctrl.Result{}, nil
	}
	grace := teardownGrace(lease)
	// a stamp from another replica's clock can read ahead of now, so a zero grace must not depend on the elapsed test
	if grace <= 0 {
		return ctrl.Result{}, nil
	}
	if !workloadReleased(lease) {
		return ctrl.Result{}, nil
	}
	if r.remainingTeardownBudget(lease) <= reservedDrainBudget(lease) {
		return ctrl.Result{}, nil
	}

	namespaces, err := everyWorkloadInNamespaces(lease)
	if err != nil {
		return ctrl.Result{}, err
	}
	restored, err := k8s.WorkloadOffBurstNodes(ctx, r.Kube, namespaces, leaseIdentity(lease))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check workload restored: %w", err)
	}
	if restored {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: restoreGatePoll}, nil
}

func workloadReleased(lease *v1alpha1.CapacityLease) bool {
	if replicateMode(lease) {
		return conditionReason(lease, v1alpha1.ConditionWorkloadReplicable) == reasonCopiesDeleted
	}
	return conditionReason(lease, v1alpha1.ConditionWorkloadMigrated) == reasonPlacementRestored
}

func conditionReason(lease *v1alpha1.CapacityLease, condition string) string {
	cond := meta.FindStatusCondition(lease.Status.Conditions, condition)
	if cond == nil {
		return ""
	}
	return cond.Reason
}

func leaseIdentity(lease *v1alpha1.CapacityLease) k8s.LeaseIdentity {
	return k8s.LeaseIdentity{UID: string(lease.UID), Name: lease.Name}
}
