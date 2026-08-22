package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	instanceLaunchTimeout   = 5 * time.Minute
	nodeRegistrationTimeout = 15 * time.Minute
)

func (r *CapacityLeaseReconciler) reconcileInstances(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider, degraded *degradation) (ctrl.Result, error) {
	observed, err := prov.List(ctx, leaseSelector(lease))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("list instances of lease %q: %w", lease.Name, err)
	}

	var records metricWrites
	changed, err := r.adoptObservedInstances(ctx, lease, prov, observed, &records)
	if err != nil {
		return ctrl.Result{}, err
	}
	if changed {
		if err := r.writeStatus(ctx, lease, records...); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: stepRequeue}, nil
	}

	if res, err := r.retireStalledInstances(ctx, lease, prov, degraded); err != nil || !res.IsZero() {
		return res, err
	}

	name, entry, pending := pendingSlot(lease)
	if !pending {
		return ctrl.Result{}, nil
	}
	if entry == nil || entry.Phase != v1alpha1.InstancePhaseIntended {
		return r.recordIntent(ctx, lease, name, entry)
	}
	return r.createInstance(ctx, lease, prov, entry)
}

func (r *CapacityLeaseReconciler) adoptObservedInstances(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider, observed []provider.Instance, records *metricWrites) (bool, error) {
	unmatched := make(map[string]provider.Instance, len(observed))
	for _, inst := range observed {
		unmatched[inst.Name] = inst
	}

	changed := false
	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		inst, present := unmatched[entry.Name]
		delete(unmatched, entry.Name)

		switch {
		case !present:
			vanished, err := r.adoptVanishedInstance(ctx, lease, prov, entry, records)
			if err != nil {
				return changed, err
			}
			changed = changed || vanished
		case entry.Phase == v1alpha1.InstancePhaseIntended || entry.Phase == v1alpha1.InstancePhaseReleased:
			markCreated(entry, inst.ProviderID)
			changed = true
		case entry.ProviderID != inst.ProviderID:
			entry.ProviderID = inst.ProviderID
			changed = true
		}
	}

	for _, name := range slices.Sorted(maps.Keys(unmatched)) {
		inst := unmatched[name]
		lease.Status.Instances = append(lease.Status.Instances, v1alpha1.InstanceStatus{
			Name:       inst.Name,
			ProviderID: inst.ProviderID,
			Phase:      v1alpha1.InstancePhaseCreated,
			CreatedAt:  &metav1.Time{Time: inst.CreatedAt},
		})
		changed = true
	}
	return changed, nil
}

// an empty but successful listing would otherwise bill a lifetime the instance keeps accruing
func (r *CapacityLeaseReconciler) adoptVanishedInstance(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider, entry *v1alpha1.InstanceStatus, records *metricWrites) (bool, error) {
	if entry.Phase == v1alpha1.InstancePhaseIntended || entry.Phase == v1alpha1.InstancePhaseReleased {
		return false, nil
	}
	absent, err := provider.ConfirmAbsent(ctx, prov, entry.Name)
	if err != nil {
		return false, fmt.Errorf("confirm instance %q of lease %q is gone: %w", entry.Name, lease.Name, err)
	}
	if !absent {
		return false, nil
	}

	entry.Phase = v1alpha1.InstancePhaseReleased
	records.add(r.instanceReleaseRecord(lease, *entry, vanishedPath(lease, r.now())))
	return true, nil
}

// the watchdog only fires once its own deadline has passed, so an earlier disappearance came from outside horizon
func vanishedPath(lease *v1alpha1.CapacityLease, now time.Time) metrics.Path {
	deadline := lease.Status.WatchdogDeadline
	if deadline.IsZero() || !now.Before(deadline.Time) {
		return metrics.PathNode
	}
	return metrics.PathExternal
}

func pendingSlot(lease *v1alpha1.CapacityLease) (string, *v1alpha1.InstanceStatus, bool) {
	for ordinal := range int(lease.Spec.Replicas) {
		name := instanceName(lease, ordinal)
		entry := findInstance(lease, name)
		switch {
		case entry == nil:
			return name, nil, true
		case entry.Phase == v1alpha1.InstancePhaseIntended:
			return name, entry, true
		case entry.Phase == v1alpha1.InstancePhaseReleased && entry.LastError == "":
			return name, entry, true
		}
	}
	return "", nil, false
}

func instanceName(lease *v1alpha1.CapacityLease, ordinal int) string {
	return fmt.Sprintf("%s-%d", lease.Name, ordinal)
}

func findInstance(lease *v1alpha1.CapacityLease, name string) *v1alpha1.InstanceStatus {
	for i := range lease.Status.Instances {
		if lease.Status.Instances[i].Name == name {
			return &lease.Status.Instances[i]
		}
	}
	return nil
}

func (r *CapacityLeaseReconciler) recordIntent(ctx context.Context, lease *v1alpha1.CapacityLease, name string, entry *v1alpha1.InstanceStatus) (ctrl.Result, error) {
	intended := v1alpha1.InstanceStatus{
		Name:      name,
		Phase:     v1alpha1.InstancePhaseIntended,
		CreatedAt: &metav1.Time{Time: r.now()},
	}
	if entry == nil {
		lease.Status.Instances = append(lease.Status.Instances, intended)
	} else {
		*entry = intended
	}
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

// a lease admitted against a cold catalogue latches nothing, and its pinned size is immutable for the life of the lease
func machineType(lease *v1alpha1.CapacityLease) string {
	if lease.Status.InstanceType != "" {
		return lease.Status.InstanceType
	}
	return lease.Spec.Size
}

// only a node reconcile recomputes the stage, and a lease whose creates keep failing never reaches one
func markCreated(entry *v1alpha1.InstanceStatus, providerID string) {
	entry.Phase = v1alpha1.InstancePhaseCreated
	entry.ProviderID = providerID
	entry.Stage = ""
	entry.LastError = ""
}

func (r *CapacityLeaseReconciler) createInstance(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider, entry *v1alpha1.InstanceStatus) (ctrl.Result, error) {
	inst, createErr := prov.Create(ctx, provider.CreateRequest{
		Name:   entry.Name,
		Region: lease.Spec.Region,
		Size:   machineType(lease),
		Labels: instanceLabels(lease),
	})
	if createErr != nil {
		createErr = fmt.Errorf("create instance %q: %w", entry.Name, createErr)
		entry.LastError = createErr.Error()
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, errors.Join(createErr, err)
		}
		return ctrl.Result{}, createErr
	}

	markCreated(entry, inst.ProviderID)
	entry.CreatedAt = &metav1.Time{Time: inst.CreatedAt}
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func (r *CapacityLeaseReconciler) retireStalledInstances(ctx context.Context, lease *v1alpha1.CapacityLease, prov provider.Provider, degraded *degradation) (ctrl.Result, error) {
	now := r.now()
	var records metricWrites
	changed := false
	var firstErr error

	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		reason, message, stalled := stallReason(entry, now)
		if !stalled {
			continue
		}
		changed = true
		if err := r.releaseInstance(ctx, lease, prov, entry, teardownGrace(lease), &records); !releaseSucceeded(err) {
			firstErr = errors.Join(firstErr, err)
			continue
		}
		entry.LastError = message
		degraded.observe(reason, message)
	}

	if !changed {
		return ctrl.Result{}, nil
	}
	if err := r.writeStatus(ctx, lease, records...); err != nil {
		return ctrl.Result{}, errors.Join(firstErr, err)
	}
	if firstErr != nil {
		return ctrl.Result{}, firstErr
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func stallReason(entry *v1alpha1.InstanceStatus, now time.Time) (reason, message string, stalled bool) {
	if entry.CreatedAt == nil {
		return "", "", false
	}
	waited := now.Sub(entry.CreatedAt.Time)
	switch {
	case entry.Phase == v1alpha1.InstancePhaseIntended && waited >= instanceLaunchTimeout:
		return reasonLaunchTimeout, fmt.Sprintf("instance %q did not launch within %s", entry.Name, instanceLaunchTimeout), true
	case entry.Phase == v1alpha1.InstancePhaseCreated && waited >= nodeRegistrationTimeout:
		return reasonRegistrationTimeout, fmt.Sprintf("node for instance %q did not register within %s", entry.Name, nodeRegistrationTimeout), true
	default:
		return "", "", false
	}
}
