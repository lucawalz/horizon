// Package controller reconciles the horizon.dev custom resources.
package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const capacityLeaseControllerName = "capacitylease"

type CapacityLeaseReconciler struct {
	Client client.Client
}

func (r *CapacityLeaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *CapacityLeaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CapacityLease{}).
		Named(capacityLeaseControllerName).
		Complete(r)
}
