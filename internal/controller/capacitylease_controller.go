// Package controller reconciles the horizon.dev custom resources.
package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	CapacityLeaseControllerName = "capacitylease"
	capacityLeaseFinalizer      = "horizon.dev/capacity-lease"

	LeaseNameLabelKey = "horizon.dev/lease"
	LeaseUIDLabelKey  = provider.LeaseUIDLabelKey

	leasePollInterval = 30 * time.Second
	stepRequeue       = 250 * time.Millisecond

	defaultTeardownGrace = 2 * time.Minute
)

const (
	reasonAccepted            = "Accepted"
	reasonProviderUnavailable = "ProviderUnavailable"
	reasonDeadlineReached     = "DeadlineReached"
	reasonNodesReady          = "NodesReady"
	reasonWaitingForNodes     = "WaitingForNodes"
	reasonMigrated            = "Migrated"
	reasonMigrateFailed       = "MigrateFailed"
	reasonPlacementRestored   = "PlacementRestored"
	reasonRestoreFailed       = "RestoreFailed"
	reasonReleased            = "Released"
	reasonReleasePending      = "ReleasePending"
	reasonReleaseFailed       = "ReleaseFailed"
	reasonLaunchTimeout       = "LaunchTimeout"
	reasonRegistrationTimeout = "RegistrationTimeout"
	reasonWatchdogArmed       = "WatchdogArmed"
	reasonWatchdogUnarmed     = "WatchdogUnarmed"

	actionMarkedWatchdogUnarmed = "MarkedWatchdogUnarmed"
)

type ProviderFactory func(ctx context.Context, cfg *v1alpha1.ProviderConfig) (provider.Provider, error)

type CapacityLeaseReconciler struct {
	Client   client.Client
	Kube     kubernetes.Interface
	Provider ProviderFactory
	Clock    func() time.Time
	Recorder events.EventRecorder
}

func (r *CapacityLeaseReconciler) now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock()
}

func (r *CapacityLeaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lease := &v1alpha1.CapacityLease{}
	if err := r.Client.Get(ctx, req.NamespacedName, lease); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !lease.DeletionTimestamp.IsZero() {
		return r.teardown(ctx, lease)
	}

	added, err := r.ensureFinalizer(ctx, lease)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{RequeueAfter: stepRequeue}, nil
	}

	cfg, prov, err := r.providerFor(ctx, lease)
	if err != nil {
		return r.rejectLease(ctx, lease, err)
	}
	policy := cfg.Spec.Watchdog

	if lease.Status.AcceptedAt == nil {
		if err := requireTeardownGuarantee(cfg, prov); err != nil {
			return r.rejectLease(ctx, lease, err)
		}
		return r.acceptLease(ctx, lease)
	}

	if !r.now().Before(lease.Status.ExpiresAt.Time) {
		return r.expire(ctx, lease)
	}

	if res, err := r.reconcileInstances(ctx, lease, prov); err != nil || !res.IsZero() {
		return res, err
	}
	if res, err := r.reconcileNodes(ctx, lease, policy); err != nil || !res.IsZero() {
		return res, err
	}
	if res, err := r.reconcileWorkload(ctx, lease, policy); err != nil || !res.IsZero() {
		return res, err
	}

	return r.nextPoll(lease, policy), nil
}

func (r *CapacityLeaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CapacityLease{}).
		Named(CapacityLeaseControllerName).
		Complete(r)
}

func (r *CapacityLeaseReconciler) ensureFinalizer(ctx context.Context, lease *v1alpha1.CapacityLease) (bool, error) {
	if !controllerutil.AddFinalizer(lease, capacityLeaseFinalizer) {
		return false, nil
	}
	if err := r.Client.Update(ctx, lease); err != nil {
		return false, fmt.Errorf("add finalizer to lease %q: %w", lease.Name, err)
	}
	return true, nil
}

func (r *CapacityLeaseReconciler) providerFor(ctx context.Context, lease *v1alpha1.CapacityLease) (*v1alpha1.ProviderConfig, provider.Provider, error) {
	if r.Provider == nil {
		return nil, nil, errors.New("no provider factory configured")
	}
	cfg := &v1alpha1.ProviderConfig{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: lease.Spec.ProviderRef}, cfg); err != nil {
		return nil, nil, fmt.Errorf("get providerconfig %q: %w", lease.Spec.ProviderRef, err)
	}
	prov, err := r.Provider(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build provider from %q: %w", cfg.Name, err)
	}
	return cfg, prov, nil
}

func (r *CapacityLeaseReconciler) rejectLease(ctx context.Context, lease *v1alpha1.CapacityLease, cause error) (ctrl.Result, error) {
	setCondition(lease, v1alpha1.ConditionAccepted, metav1.ConditionFalse, reasonProviderUnavailable, cause.Error())
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, errors.Join(cause, err)
	}
	return ctrl.Result{}, cause
}

func (r *CapacityLeaseReconciler) acceptLease(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	accepted := r.now()
	lease.Status.AcceptedAt = &metav1.Time{Time: accepted}
	lease.Status.ExpiresAt = &metav1.Time{Time: accepted.Add(lease.Spec.Duration.Duration)}
	if lease.Status.InstanceType == "" {
		lease.Status.InstanceType = lease.Spec.Size
	}
	setCondition(lease, v1alpha1.ConditionAccepted, metav1.ConditionTrue, reasonAccepted, "lease accepted and deadline recorded")
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func (r *CapacityLeaseReconciler) expire(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if setCondition(lease, v1alpha1.ConditionExpired, metav1.ConditionTrue, reasonDeadlineReached, "lease deadline reached") {
		if err := r.writeStatus(ctx, lease); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.teardown(ctx, lease)
}

func (r *CapacityLeaseReconciler) nextPoll(lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) ctrl.Result {
	after := leasePollInterval
	if renew := policy.RenewInterval.Duration; renew > 0 && renew < after {
		after = renew
	}
	if lease.Status.ExpiresAt != nil {
		if remaining := lease.Status.ExpiresAt.Sub(r.now()); remaining > 0 && remaining < after {
			after = remaining
		}
	}
	return ctrl.Result{RequeueAfter: after}
}

func (r *CapacityLeaseReconciler) writeStatus(ctx context.Context, lease *v1alpha1.CapacityLease) error {
	lease.Status.Phase = derivePhase(lease)
	lease.Status.ObservedGeneration = lease.Generation

	desired := lease.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		updateErr := r.Client.Status().Update(ctx, lease)
		if !apierrors.IsConflict(updateErr) {
			return updateErr
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
			return err
		}
		lease.Status = desired
		return updateErr
	})
	if err != nil {
		return fmt.Errorf("update status of lease %q: %w", lease.Name, err)
	}
	return nil
}

func setCondition(lease *v1alpha1.CapacityLease, condition string, status metav1.ConditionStatus, reason, message string) bool {
	return meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{
		Type:               condition,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: lease.Generation,
	})
}

func conditionTrue(lease *v1alpha1.CapacityLease, condition string) bool {
	return meta.IsStatusConditionTrue(lease.Status.Conditions, condition)
}

func derivePhase(lease *v1alpha1.CapacityLease) v1alpha1.LeasePhase {
	switch {
	case conditionTrue(lease, v1alpha1.ConditionReleased):
		return v1alpha1.LeasePhaseReleased
	case conditionTrue(lease, v1alpha1.ConditionDegraded):
		return v1alpha1.LeasePhaseDegraded
	case conditionTrue(lease, v1alpha1.ConditionExpired) || !lease.DeletionTimestamp.IsZero():
		return v1alpha1.LeasePhaseExpiring
	case conditionTrue(lease, v1alpha1.ConditionInstancesReady):
		return v1alpha1.LeasePhaseActive
	case conditionTrue(lease, v1alpha1.ConditionAccepted):
		return v1alpha1.LeasePhaseProvisioning
	default:
		return v1alpha1.LeasePhasePending
	}
}

func teardownGrace(lease *v1alpha1.CapacityLease) time.Duration {
	if lease.Spec.TeardownGrace == nil {
		return defaultTeardownGrace
	}
	return lease.Spec.TeardownGrace.Duration
}

func leaseSelector(lease *v1alpha1.CapacityLease) map[string]string {
	return map[string]string{LeaseUIDLabelKey: string(lease.UID)}
}

func instanceLabels(lease *v1alpha1.CapacityLease) map[string]string {
	return map[string]string{
		provider.ManagedByLabelKey: provider.ManagedByValue,
		provider.PoolLabelKey:      provider.ReservedPoolValue,
		provider.ExpiresAtLabelKey: provider.FormatExpiry(lease.Status.ExpiresAt.Time),
		LeaseNameLabelKey:          lease.Name,
		LeaseUIDLabelKey:           string(lease.UID),
	}
}
