package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

var errReleaseDegraded = errors.New("release completed with a skipped step")

func (r *CapacityLeaseReconciler) teardown(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if res, err := r.restoreWorkload(ctx, lease); err != nil || !res.IsZero() {
		return res, err
	}
	if res, err := r.awaitWorkloadRestored(ctx, lease); err != nil || !res.IsZero() {
		return res, err
	}

	if hasUnreleasedInstances(lease) {
		_, prov, err := r.providerFor(ctx, lease)
		if err != nil {
			return r.degrade(ctx, lease, reasonProviderUnavailable, err)
		}
		if res, err := r.releaseInstances(ctx, lease, prov); err != nil || !res.IsZero() {
			return res, err
		}
	}

	return r.finishTeardown(ctx, lease)
}

func (r *CapacityLeaseReconciler) releaseInstances(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider) (ctrl.Result, error) {
	grace := teardownGrace(lease)
	var records metricWrites
	var blocking error

	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		if entry.Phase == v1alpha1.InstancePhaseReleased {
			continue
		}
		err := r.releaseInstance(ctx, lease, prov, entry, grace, &records)
		switch {
		case err == nil:
		case errors.Is(err, errReleaseDegraded):
			setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, reasonReleaseFailed, err.Error())
		default:
			blocking = errors.Join(blocking, err)
			setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, reasonReleaseFailed, err.Error())
			setCondition(lease, v1alpha1.ConditionReleased, metav1.ConditionFalse, reasonReleasePending,
				"instances remain until every deletion is confirmed absent")
		}
	}

	if err := r.writeStatus(ctx, lease, records...); err != nil {
		return ctrl.Result{}, errors.Join(blocking, err)
	}
	if blocking != nil {
		return ctrl.Result{}, blocking
	}
	if hasUnreleasedInstances(lease) {
		return ctrl.Result{RequeueAfter: stepRequeue}, nil
	}
	return ctrl.Result{}, nil
}

func (r *CapacityLeaseReconciler) releaseInstance(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider, entry *v1alpha1.InstanceStatus, grace time.Duration, records *metricWrites) error {
	if entry.NodeName != "" {
		entry.Phase = v1alpha1.InstancePhaseDraining
	}

	skipped := r.drainNode(ctx, entry.NodeName, grace)

	if err := prov.Delete(ctx, entry.Name); err != nil {
		entry.LastError = err.Error()
		return fmt.Errorf("delete instance %q: %w", entry.Name, err)
	}

	if _, err := prov.Get(ctx, entry.Name); !errors.Is(err, provider.ErrNotFound) {
		if err == nil {
			err = fmt.Errorf("instance %q is still reported by the provider", entry.Name)
		}
		entry.LastError = err.Error()
		return fmt.Errorf("confirm release of instance %q: %w", entry.Name, err)
	}

	if err := r.deleteOwnedNode(ctx, lease, entry.NodeName); err != nil {
		if !errors.Is(err, errReleaseDegraded) {
			entry.LastError = err.Error()
			return err
		}
		skipped = errors.Join(skipped, err)
	}

	markReleased(entry)
	records.add(r.instanceReleaseRecord(lease, *entry, metrics.PathController))
	if skipped != nil {
		entry.LastError = skipped.Error()
		return skipped
	}
	entry.LastError = ""
	return nil
}

func (r *CapacityLeaseReconciler) drainNode(ctx context.Context, nodeName string, grace time.Duration) error {
	if nodeName == "" || grace <= 0 {
		return nil
	}
	err := k8s.Drain(ctx, r.Kube, nodeName, grace, nil)
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("drain node %q within %s: %v: %w", nodeName, grace, err, errReleaseDegraded)
}

func (r *CapacityLeaseReconciler) finishTeardown(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	var records metricWrites
	changed := setCondition(lease, v1alpha1.ConditionReleased, metav1.ConditionTrue, reasonReleased, "every instance is confirmed absent")
	if changed && lease.Status.AcceptedAt != nil {
		records.add(terminalRecord(attributionOf(lease), releaseOutcome(lease)))
	}
	changed = setCondition(lease, v1alpha1.ConditionInstancesReady, metav1.ConditionFalse, reasonReleased, "capacity released") || changed
	changed = setCondition(lease, v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse, reasonReleased,
		"no joined node remains to report an armed watchdog") || changed
	if lease.Status.ReleasedAt == nil {
		released := r.now()
		lease.Status.ReleasedAt = &metav1.Time{Time: released}
		changed = true
		records.add(releaseDurationRecord(lease, released))
	}
	if changed {
		if err := r.writeStatus(ctx, lease, records...); err != nil {
			return ctrl.Result{}, err
		}
	}

	if lease.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if controllerutil.RemoveFinalizer(lease, capacityLeaseFinalizer) {
		if err := r.Client.Update(ctx, lease); err != nil {
			return ctrl.Result{}, fmt.Errorf("remove finalizer from lease %q: %w", lease.Name, err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *CapacityLeaseReconciler) degrade(ctx context.Context, lease *v1alpha1.CapacityLease, reason string, cause error) (ctrl.Result, error) {
	setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, reason, cause.Error())
	setCondition(lease, v1alpha1.ConditionReleased, metav1.ConditionFalse, reason, "capacity is still held")
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, errors.Join(cause, err)
	}
	return ctrl.Result{}, cause
}

func releaseOutcome(lease *v1alpha1.CapacityLease) metrics.Outcome {
	if conditionTrue(lease, v1alpha1.ConditionDegraded) {
		return metrics.OutcomeReleasedDegraded
	}
	return metrics.OutcomeReleased
}

func releaseDurationRecord(lease *v1alpha1.CapacityLease, released time.Time) func() {
	start, due := teardownStart(lease)
	if !due || !heldCapacity(lease) {
		return nil
	}
	// the deletion timestamp is stamped by the API server's clock, which can run ahead of the controller's
	took := max(released.Sub(start), 0)
	return releaseRecord(attributionOf(lease), took)
}

func teardownStart(lease *v1alpha1.CapacityLease) (time.Time, bool) {
	var start time.Time
	for _, due := range []*metav1.Time{lease.DeletionTimestamp, lease.Status.ExpiresAt} {
		if due.IsZero() {
			continue
		}
		if start.IsZero() || due.Time.Before(start) {
			start = due.Time
		}
	}
	return start, !start.IsZero()
}

func (r *CapacityLeaseReconciler) instanceReleaseRecord(lease *v1alpha1.CapacityLease, entry v1alpha1.InstanceStatus, path metrics.Path) func() {
	if !existedAtProvider(entry) {
		return nil
	}
	return releasedInstanceRecord(attributionOf(lease), path, createdInstant(entry), r.now())
}

func createdInstant(entry v1alpha1.InstanceStatus) time.Time {
	if entry.CreatedAt == nil {
		return time.Time{}
	}
	return entry.CreatedAt.Time
}

func existedAtProvider(entry v1alpha1.InstanceStatus) bool {
	return entry.ProviderID != ""
}

func heldCapacity(lease *v1alpha1.CapacityLease) bool {
	for _, entry := range lease.Status.Instances {
		if existedAtProvider(entry) {
			return true
		}
	}
	return false
}

func hasUnreleasedInstances(lease *v1alpha1.CapacityLease) bool {
	for _, entry := range lease.Status.Instances {
		if entry.Phase != v1alpha1.InstancePhaseReleased {
			return true
		}
	}
	return false
}

func releaseSucceeded(err error) bool {
	return err == nil || errors.Is(err, errReleaseDegraded)
}
