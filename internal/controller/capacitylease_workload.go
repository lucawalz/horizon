package controller

import (
	"context"
	"errors"
	"fmt"

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

	namespace := lease.Spec.Workload.Namespace
	if !conditionTrue(lease, v1alpha1.ConditionWorkloadMigrated) {
		return r.migrateWorkload(ctx, lease, namespace)
	}

	placed, err := k8s.WorkloadOnBurstNodes(ctx, r.Kube, namespace, leaseIdentity(lease))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check workload placement in %q: %w", namespace, err)
	}
	if !placed {
		return r.nextPoll(lease, policy), nil
	}
	return ctrl.Result{}, nil
}

func (r *CapacityLeaseReconciler) migrateWorkload(ctx context.Context, lease *v1alpha1.CapacityLease, namespace string) (ctrl.Result, error) {
	assessments, classifyErr := k8s.ClassifyMigratability(ctx, r.Kube, namespace, leaseIdentity(lease))
	r.recordMigratability(lease, assessments, classifyErr)

	migrated, migrateErr := k8s.Migrate(ctx, r.Kube, namespace, leaseIdentity(lease))
	if migrateErr != nil {
		migrateErr = fmt.Errorf("migrate workload in %q: %w", namespace, migrateErr)
		r.setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse, reasonMigrateFailed, migrateErr.Error())
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, errors.Join(migrateErr, err)
		}
		return ctrl.Result{}, migrateErr
	}

	lease.Status.MigratedWorkloads = migrated
	r.setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue, reasonMigrated,
		fmt.Sprintf("%d workloads moved onto burst capacity", len(migrated)))
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
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
		fmt.Sprintf("%d of %d workloads lose availability while moving onto burst capacity", len(warnings), len(assessments)))
}

func (r *CapacityLeaseReconciler) restoreWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Spec.Workload == nil || !conditionTrue(lease, v1alpha1.ConditionWorkloadMigrated) {
		return ctrl.Result{}, nil
	}

	namespace := lease.Spec.Workload.Namespace
	if _, restoreErr := k8s.RestorePlacement(ctx, r.Kube, namespace, leaseIdentity(lease)); restoreErr != nil {
		restoreErr = fmt.Errorf("restore placement in %q: %w", namespace, restoreErr)
		r.setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, reasonRestoreFailed, restoreErr.Error())
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, errors.Join(restoreErr, err)
		}
		return ctrl.Result{}, restoreErr
	}

	lease.Status.MigratedWorkloads = nil
	lease.Status.MigrationWarnings = nil
	meta.RemoveStatusCondition(&lease.Status.Conditions, v1alpha1.ConditionWorkloadMigratable)
	r.setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse, reasonPlacementRestored,
		"workload placement restored")
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
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
	cond := meta.FindStatusCondition(lease.Status.Conditions, v1alpha1.ConditionWorkloadMigrated)
	if cond == nil || cond.Reason != reasonPlacementRestored {
		return ctrl.Result{}, nil
	}
	if r.remainingTeardownBudget(lease) <= 0 {
		return ctrl.Result{}, nil
	}

	namespace := lease.Spec.Workload.Namespace
	restored, err := k8s.WorkloadOffBurstNodes(ctx, r.Kube, namespace, leaseIdentity(lease))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check workload restored in %q: %w", namespace, err)
	}
	if restored {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: restoreGatePoll}, nil
}

func leaseIdentity(lease *v1alpha1.CapacityLease) k8s.LeaseIdentity {
	return k8s.LeaseIdentity{UID: string(lease.UID), Name: lease.Name}
}
