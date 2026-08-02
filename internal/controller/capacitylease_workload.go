package controller

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
)

func (r *CapacityLeaseReconciler) reconcileWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Spec.Workload == nil {
		return ctrl.Result{}, nil
	}
	if !conditionTrue(lease, v1alpha1.ConditionInstancesReady) {
		return r.nextPoll(lease), nil
	}

	namespace := lease.Spec.Workload.Namespace
	if !conditionTrue(lease, v1alpha1.ConditionWorkloadMigrated) {
		return r.migrateWorkload(ctx, lease, namespace)
	}

	placed, err := k8s.WorkloadOnBurstNodes(ctx, r.Kube, namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check workload placement in %q: %w", namespace, err)
	}
	if !placed {
		return r.nextPoll(lease), nil
	}
	return ctrl.Result{}, nil
}

func (r *CapacityLeaseReconciler) migrateWorkload(ctx context.Context, lease *v1alpha1.CapacityLease, namespace string) (ctrl.Result, error) {
	migrated, migrateErr := k8s.Migrate(ctx, r.Kube, namespace, provider.ReservedPoolValue)
	if len(migrated) > 0 {
		lease.Status.MigratedWorkloads = migrated
	}
	if migrateErr != nil {
		migrateErr = fmt.Errorf("migrate workload in %q: %w", namespace, migrateErr)
		setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse, reasonMigrateFailed, migrateErr.Error())
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, errors.Join(migrateErr, err)
		}
		return ctrl.Result{}, migrateErr
	}

	setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue, reasonMigrated,
		fmt.Sprintf("%d workloads moved onto burst capacity", len(migrated)))
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func (r *CapacityLeaseReconciler) restoreWorkload(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Spec.Workload == nil || !conditionTrue(lease, v1alpha1.ConditionWorkloadMigrated) {
		return ctrl.Result{}, nil
	}

	namespace := lease.Spec.Workload.Namespace
	if _, restoreErr := k8s.RestorePlacement(ctx, r.Kube, namespace); restoreErr != nil {
		restoreErr = fmt.Errorf("restore placement in %q: %w", namespace, restoreErr)
		setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, reasonRestoreFailed, restoreErr.Error())
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, errors.Join(restoreErr, err)
		}
		return ctrl.Result{}, restoreErr
	}

	lease.Status.MigratedWorkloads = nil
	setCondition(lease, v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse, reasonPlacementRestored,
		"workload placement restored")
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}
