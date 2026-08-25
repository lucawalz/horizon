package controller

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

func newConfigHarness(t *testing.T) (client.Client, *ProviderConfigReconciler, *v1alpha1.ProviderConfig) {
	t.Helper()
	api := apiServerClient(t)
	config := validProviderConfig(objectName(t))
	if err := api.Create(t.Context(), config); err != nil {
		t.Fatalf("create provider config %q: %v", config.Name, err)
	}
	t.Cleanup(func() { forceRemove(api, config.Name) })
	return api, &ProviderConfigReconciler{Client: api}, config
}

func forceRemove(api client.Client, name string) {
	// a config held by its own finalizer outlives the test unless the finalizer is stripped first
	ctx := context.Background()
	var live v1alpha1.ProviderConfig
	if err := api.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
		return
	}
	if controllerutil.RemoveFinalizer(&live, providerConfigFinalizer) {
		_ = api.Update(ctx, &live)
	}
	_ = api.Delete(ctx, &live)
}

func reconcileConfig(t *testing.T, reconciler *ProviderConfigReconciler, name string) {
	t.Helper()
	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name}}); err != nil {
		t.Fatalf("reconcile provider config %q: %v", name, err)
	}
}

func readProviderConfig(t *testing.T, api client.Client, name string) *v1alpha1.ProviderConfig {
	t.Helper()
	var live v1alpha1.ProviderConfig
	if err := api.Get(t.Context(), client.ObjectKey{Name: name}, &live); err != nil {
		t.Fatalf("read provider config %q: %v", name, err)
	}
	return &live
}

func bindLease(t *testing.T, api client.Client, config string) *v1alpha1.CapacityLease {
	t.Helper()
	lease := validLease(t)
	lease.Spec.ProviderRef = config
	if err := api.Create(t.Context(), lease); err != nil {
		t.Fatalf("create capacity lease %q: %v", lease.Name, err)
	}
	t.Cleanup(func() { _ = api.Delete(context.Background(), lease) })
	return lease
}

func TestProviderConfigTakesAFinalizerBeforeAnythingBindsToIt(t *testing.T) {
	api, reconciler, config := newConfigHarness(t)

	reconcileConfig(t, reconciler, config.Name)

	if !controllerutil.ContainsFinalizer(readProviderConfig(t, api, config.Name), providerConfigFinalizer) {
		t.Errorf("provider config %q carries no finalizer, want one before a lease can bind to it", config.Name)
	}
}

func TestProviderConfigIsHeldWhileALeaseNamesIt(t *testing.T) {
	api, reconciler, config := newConfigHarness(t)
	reconcileConfig(t, reconciler, config.Name)
	lease := bindLease(t, api, config.Name)

	if err := api.Delete(t.Context(), config); err != nil {
		t.Fatalf("delete provider config %q: %v", config.Name, err)
	}
	reconcileConfig(t, reconciler, config.Name)

	held := readProviderConfig(t, api, config.Name)
	if !controllerutil.ContainsFinalizer(held, providerConfigFinalizer) {
		t.Fatalf("provider config %q released its finalizer while %q names it", config.Name, lease.Name)
	}

	refusal := meta.FindStatusCondition(held.Status.Conditions, v1alpha1.ConditionDeletable)
	if refusal == nil {
		t.Fatalf("provider config %q reports no %s condition, want the refusal readable without the logs", config.Name, v1alpha1.ConditionDeletable)
	}
	if refusal.Reason != reasonLeasesBound {
		t.Errorf("reason = %q, want %q", refusal.Reason, reasonLeasesBound)
	}
	if !strings.Contains(refusal.Message, lease.Name) {
		t.Errorf("message = %q, want it to name the lease holding the config", refusal.Message)
	}
}

func TestProviderConfigIsReleasedOnceNoLeaseNamesIt(t *testing.T) {
	api, reconciler, config := newConfigHarness(t)
	reconcileConfig(t, reconciler, config.Name)
	lease := bindLease(t, api, config.Name)

	if err := api.Delete(t.Context(), config); err != nil {
		t.Fatalf("delete provider config %q: %v", config.Name, err)
	}
	reconcileConfig(t, reconciler, config.Name)
	if err := api.Delete(t.Context(), lease); err != nil {
		t.Fatalf("delete capacity lease %q: %v", lease.Name, err)
	}
	reconcileConfig(t, reconciler, config.Name)

	err := api.Get(t.Context(), client.ObjectKey{Name: config.Name}, &v1alpha1.ProviderConfig{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("reading the released config answered %v, want a not-found", err)
	}
}

func TestProviderConfigIgnoresALeaseNamingAnotherConfig(t *testing.T) {
	api, reconciler, config := newConfigHarness(t)
	reconcileConfig(t, reconciler, config.Name)
	bindLease(t, api, "another-provider")

	if err := api.Delete(t.Context(), config); err != nil {
		t.Fatalf("delete provider config %q: %v", config.Name, err)
	}
	reconcileConfig(t, reconciler, config.Name)

	err := api.Get(t.Context(), client.ObjectKey{Name: config.Name}, &v1alpha1.ProviderConfig{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("reading the released config answered %v, want a not-found", err)
	}
}
