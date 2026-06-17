package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlplanev1alpha1 "github.com/lucawalz/horizon/api/controlplane/v1alpha1"
)

type ExternalControlPlaneReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=controlplane.horizon.dev,resources=externalcontrolplanes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=controlplane.horizon.dev,resources=externalcontrolplanes/status,verbs=get;update;patch

func (r *ExternalControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cp := &controlplanev1alpha1.ExternalControlPlane{}
	if err := r.Get(ctx, req.NamespacedName, cp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	desired := cp.DeepCopy()
	applyExternallyManagedStatus(desired)

	if equality.Semantic.DeepEqual(cp.Status, desired.Status) {
		return ctrl.Result{}, nil
	}

	if err := r.Status().Patch(ctx, desired, client.MergeFrom(cp)); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func applyExternallyManagedStatus(cp *controlplanev1alpha1.ExternalControlPlane) {
	t := true
	cp.Status.Initialization.ControlPlaneInitialized = &t
	cp.Status.ExternalManagedControlPlane = &t
	cp.Status.Initialized = true
	cp.Status.Ready = true
	cp.Status.Version = cp.Spec.Version
	cp.Status.ObservedGeneration = cp.Generation
}

func (r *ExternalControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlplanev1alpha1.ExternalControlPlane{}).
		Complete(r)
}
