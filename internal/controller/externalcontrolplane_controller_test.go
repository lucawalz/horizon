package controller_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlplanev1alpha1 "github.com/lucawalz/horizon/api/controlplane/v1alpha1"
	"github.com/lucawalz/horizon/internal/controller"
)

func mustScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := controlplanev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register scheme: %v", err)
	}
	return s
}

func reconcile(t *testing.T, cp *controlplanev1alpha1.ExternalControlPlane) (*controlplanev1alpha1.ExternalControlPlane, client.Client) {
	t.Helper()
	cl := fake.NewClientBuilder().
		WithScheme(mustScheme(t)).
		WithObjects(cp).
		WithStatusSubresource(&controlplanev1alpha1.ExternalControlPlane{}).
		Build()
	r := &controller.ExternalControlPlaneReconciler{Client: cl}
	key := types.NamespacedName{Name: cp.Name, Namespace: cp.Namespace}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &controlplanev1alpha1.ExternalControlPlane{}
	if err := cl.Get(context.Background(), key, got); err != nil {
		t.Fatalf("get after reconcile: %v", err)
	}
	return got, cl
}

func newCP() *controlplanev1alpha1.ExternalControlPlane {
	return &controlplanev1alpha1.ExternalControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", Namespace: "caph-system", Generation: 3},
		Spec: controlplanev1alpha1.ExternalControlPlaneSpec{
			ControlPlaneEndpoint: controlplanev1alpha1.APIEndpoint{Host: "10.20.0.10", Port: 6443},
			Version:              "v1.35.2+k3s1",
		},
	}
}

func TestReconcileSetsContractStatus(t *testing.T) {
	got, _ := reconcile(t, newCP())

	if got.Status.Initialization.ControlPlaneInitialized == nil || !*got.Status.Initialization.ControlPlaneInitialized {
		t.Error("controlPlaneInitialized not true")
	}
	if got.Status.ExternalManagedControlPlane == nil || !*got.Status.ExternalManagedControlPlane {
		t.Error("externalManagedControlPlane not true")
	}
	if !got.Status.Ready {
		t.Error("ready not true")
	}
	if !got.Status.Initialized {
		t.Error("initialized not true")
	}
	if got.Status.Version != "v1.35.2+k3s1" {
		t.Errorf("status.version = %q, want mirrored spec version", got.Status.Version)
	}
	if got.Status.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want 3", got.Status.ObservedGeneration)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	got, cl := reconcile(t, newCP())
	rv := got.ResourceVersion

	r := &controller.ExternalControlPlaneReconciler{Client: cl}
	key := types.NamespacedName{Name: "burst", Namespace: "caph-system"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	after := &controlplanev1alpha1.ExternalControlPlane{}
	if err := cl.Get(context.Background(), key, after); err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.ResourceVersion != rv {
		t.Errorf("status patched on no-op reconcile: rv %s -> %s", rv, after.ResourceVersion)
	}
}

func TestReconcileMissingObjectNoError(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	r := &controller.ExternalControlPlaneReconciler{Client: cl}
	key := types.NamespacedName{Name: "absent", Namespace: "caph-system"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile missing object: %v", err)
	}
}
