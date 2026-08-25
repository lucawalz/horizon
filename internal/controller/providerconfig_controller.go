package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	ProviderConfigControllerName = "providerconfig"
	providerConfigFinalizer      = "horizon.dev/provider-config"
)

const reasonLeasesBound = "LeasesBound"

type ProviderConfigReconciler struct {
	Client client.Client
}

func (r *ProviderConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	config := &v1alpha1.ProviderConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, config); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if config.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.ensureFinalizer(ctx, config)
	}
	return ctrl.Result{}, r.release(ctx, config)
}

func (r *ProviderConfigReconciler) ensureFinalizer(ctx context.Context, config *v1alpha1.ProviderConfig) error {
	if !controllerutil.AddFinalizer(config, providerConfigFinalizer) {
		return nil
	}
	if err := r.Client.Update(ctx, config); err != nil {
		return fmt.Errorf("add finalizer to provider config %q: %w", config.Name, err)
	}
	return nil
}

func (r *ProviderConfigReconciler) release(ctx context.Context, config *v1alpha1.ProviderConfig) error {
	// horizon tears a lease down with the credentials this configuration resolves, so the deletion waits for the last lease rather than racing it
	var leases v1alpha1.CapacityLeaseList
	if err := r.Client.List(ctx, &leases); err != nil {
		return fmt.Errorf("list the capacity leases bound to provider config %q: %w", config.Name, err)
	}
	if bound := leases.NamesBoundTo(config.Name); len(bound) > 0 {
		return r.reportBound(ctx, config, bound)
	}

	if !controllerutil.RemoveFinalizer(config, providerConfigFinalizer) {
		return nil
	}
	if err := r.Client.Update(ctx, config); err != nil {
		return fmt.Errorf("remove finalizer from provider config %q: %w", config.Name, err)
	}
	return nil
}

func (r *ProviderConfigReconciler) reportBound(ctx context.Context, config *v1alpha1.ProviderConfig, bound []string) error {
	refusal := metav1.Condition{
		Type:               v1alpha1.ConditionDeletable,
		Status:             metav1.ConditionFalse,
		Reason:             reasonLeasesBound,
		Message:            boundMessage(bound),
		ObservedGeneration: config.Generation,
	}
	if !meta.SetStatusCondition(&config.Status.Conditions, refusal) {
		return nil
	}
	if err := r.Client.Status().Update(ctx, config); err != nil {
		return fmt.Errorf("report the leases bound to provider config %q: %w", config.Name, err)
	}
	return nil
}

func boundMessage(bound []string) string {
	return fmt.Sprintf("%s still name this provider config, and horizon tears a lease down with the credentials it "+
		"resolves from here, so the deletion is held until no lease names it", strings.Join(bound, ", "))
}

func (r *ProviderConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ProviderConfig{}).
		Watches(&v1alpha1.CapacityLease{}, handler.EnqueueRequestsFromMapFunc(configOfLease)).
		Named(ProviderConfigControllerName).
		Complete(r)
}

func configOfLease(_ context.Context, obj client.Object) []reconcile.Request {
	// the last lease to go is what releases the config, and only a lease event says so
	lease, isLease := obj.(*v1alpha1.CapacityLease)
	if !isLease || lease.Spec.ProviderRef == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: lease.Spec.ProviderRef}}}
}
