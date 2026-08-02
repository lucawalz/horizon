package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

const (
	orphanTestFinalizer = "horizon.dev/test-teardown"
	orphanWaitTimeout   = 5 * time.Second
	orphanPollInterval  = 10 * time.Millisecond
	primaryProviderName = "primary"
)

var (
	registerNodeType       sync.Once
	errProviderUnavailable = errors.New("fake: provider unavailable")
	errProviderUnbuildable = errors.New("fake: provider cannot be built")
)

func instanceOutsideAnyLease(name string) provider.Instance {
	return provider.Instance{
		Name: name,
		Labels: map[string]string{
			provider.ManagedByLabelKey: provider.ManagedByValue,
			provider.PoolLabelKey:      provider.ReservedPoolValue,
		},
	}
}

type orphanFixture struct {
	t          *testing.T
	client     client.Client
	config     string
	provider   *fake.Provider
	providers  map[string]provider.Provider
	ledgers    []*fake.Provider
	reconciler *OrphanReconciler
	instant    time.Time
}

func newOrphanFixture(t *testing.T) *orphanFixture {
	t.Helper()

	c := apiServerClient(t)
	registerNodeType.Do(func() { utilruntime.Must(corev1.AddToScheme(c.Scheme())) })

	f := &orphanFixture{
		t:         t,
		client:    c,
		providers: map[string]provider.Provider{},
		instant:   time.Now().UTC(),
	}
	f.config, f.provider = f.addProvider(primaryProviderName)
	f.reconciler = &OrphanReconciler{Client: c, Provider: f.buildProvider, Clock: f.clock}
	return f
}

func (f *orphanFixture) clock() time.Time {
	return f.instant
}

func (f *orphanFixture) buildProvider(_ context.Context, cfg *v1alpha1.ProviderConfig) (provider.Provider, error) {
	prov, registered := f.providers[cfg.Name]
	if !registered {
		return nil, fmt.Errorf("%w: %s", errProviderUnbuildable, cfg.Name)
	}
	return prov, nil
}

func (f *orphanFixture) createProviderConfig(suffix string) string {
	f.t.Helper()

	cfg := validProviderConfig(objectName(f.t) + "-" + suffix)
	if err := f.client.Create(f.t.Context(), cfg); err != nil {
		f.t.Fatalf("create providerconfig: %v", err)
	}
	f.t.Cleanup(func() { _ = f.client.Delete(context.Background(), cfg) })
	return cfg.Name
}

func (f *orphanFixture) addProvider(suffix string) (string, *fake.Provider) {
	f.t.Helper()

	name := f.createProviderConfig(suffix)
	prov := fake.NewWithClock(f.clock)
	f.providers[name] = prov
	f.ledgers = append(f.ledgers, prov)
	return name, prov
}

func (f *orphanFixture) createLease(suffix string) *v1alpha1.CapacityLease {
	f.t.Helper()

	lease := &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: objectName(f.t) + "-" + suffix},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: "hetzner",
			Region:      "nbg1",
			Size:        "cx22",
			Replicas:    1,
			Duration:    metav1.Duration{Duration: time.Hour},
		},
	}
	if err := f.client.Create(f.t.Context(), lease); err != nil {
		f.t.Fatalf("create lease: %v", err)
	}
	f.t.Cleanup(func() { _ = f.client.Delete(context.Background(), lease) })
	return lease
}

func (f *orphanFixture) deleteLease(lease *v1alpha1.CapacityLease) {
	f.t.Helper()
	if err := f.client.Delete(f.t.Context(), lease); err != nil {
		f.t.Fatalf("delete lease: %v", err)
	}
}

func (f *orphanFixture) addFinalizer(lease *v1alpha1.CapacityLease) {
	f.t.Helper()
	lease.Finalizers = append(lease.Finalizers, orphanTestFinalizer)
	if err := f.client.Update(f.t.Context(), lease); err != nil {
		f.t.Fatalf("add finalizer: %v", err)
	}
}

func (f *orphanFixture) removeFinalizer(lease *v1alpha1.CapacityLease) {
	f.t.Helper()

	var current v1alpha1.CapacityLease
	if err := f.client.Get(f.t.Context(), client.ObjectKeyFromObject(lease), &current); err != nil {
		f.t.Fatalf("read lease: %v", err)
	}
	current.Finalizers = nil
	if err := f.client.Update(f.t.Context(), &current); err != nil {
		f.t.Fatalf("remove finalizer: %v", err)
	}
}

func (f *orphanFixture) createNode(suffix, leaseUID string, ready corev1.ConditionStatus) *corev1.Node {
	f.t.Helper()

	labels := map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue}
	if leaseUID != "" {
		labels[LeaseUIDLabelKey] = leaseUID
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: objectName(f.t) + "-" + suffix, Labels: labels}}
	if err := f.client.Create(f.t.Context(), node); err != nil {
		f.t.Fatalf("create node: %v", err)
	}
	f.t.Cleanup(func() { _ = f.client.Delete(context.Background(), node) })

	now := metav1.NewTime(f.instant)
	node.Status.Conditions = []corev1.NodeCondition{{
		Type:               corev1.NodeReady,
		Status:             ready,
		Reason:             "Test",
		LastHeartbeatTime:  now,
		LastTransitionTime: now,
	}}
	if err := f.client.Status().Update(f.t.Context(), node); err != nil {
		f.t.Fatalf("set node readiness: %v", err)
	}
	return node
}

func (f *orphanFixture) createInstance(name, leaseUID string, expiry time.Time) {
	f.t.Helper()
	f.createInstanceIn(f.provider, name, leaseUID, expiry)
}

func (f *orphanFixture) createInstanceIn(prov *fake.Provider, name, leaseUID string, expiry time.Time) {
	f.t.Helper()

	labels := map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue}
	if leaseUID != "" {
		labels[LeaseUIDLabelKey] = leaseUID
	}
	if !expiry.IsZero() {
		labels[provider.ExpiresAtLabelKey] = provider.FormatExpiry(expiry)
	}
	if _, err := prov.Create(f.t.Context(), provider.CreateRequest{Name: name, Labels: labels}); err != nil {
		f.t.Fatalf("create instance: %v", err)
	}
}

func (f *orphanFixture) deleteInstance(name string) {
	f.t.Helper()
	if err := f.provider.Delete(f.t.Context(), name); err != nil {
		f.t.Fatalf("delete instance: %v", err)
	}
}

func (f *orphanFixture) reconcileNode(node *corev1.Node) ctrl.Result {
	f.t.Helper()

	result, err := f.tryReconcileNode(node)
	if err != nil {
		f.t.Fatalf("reconcile node %s: %v", node.Name, err)
	}
	return result
}

func (f *orphanFixture) tryReconcileNode(node *corev1.Node) (ctrl.Result, error) {
	f.t.Helper()
	return f.reconciler.Reconcile(f.t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: node.Name}})
}

func (f *orphanFixture) sweep() error {
	f.t.Helper()
	return f.reconciler.sweep(f.t.Context())
}

func (f *orphanFixture) mustSweep() {
	f.t.Helper()
	if err := f.sweep(); err != nil {
		f.t.Fatalf("sweep: %v", err)
	}
}

func (f *orphanFixture) assertNodePresent(node *corev1.Node, want bool) {
	f.t.Helper()

	err := f.client.Get(f.t.Context(), client.ObjectKeyFromObject(node), &corev1.Node{})
	switch {
	case err == nil && !want:
		f.t.Errorf("node %s still exists, want deleted", node.Name)
	case err != nil && want:
		f.t.Errorf("node %s is gone, want retained: %v", node.Name, err)
	}
}

func (f *orphanFixture) instanceExists(name string) bool {
	f.t.Helper()
	return f.instanceExistsIn(f.provider, name)
}

func (f *orphanFixture) instanceExistsIn(prov *fake.Provider, name string) bool {
	f.t.Helper()

	_, err := prov.Get(context.Background(), name)
	switch {
	case err == nil:
		return true
	case errors.Is(err, provider.ErrNotFound):
		return false
	default:
		f.t.Fatalf("get instance %s: %v", name, err)
		return false
	}
}

func (f *orphanFixture) assertInstancePresent(name string, want bool) {
	f.t.Helper()
	f.assertInstancePresentIn(f.provider, name, want)
}

func (f *orphanFixture) assertInstancePresentIn(prov *fake.Provider, name string, want bool) {
	f.t.Helper()

	if got := f.instanceExistsIn(prov, name); got != want {
		f.t.Errorf("instance %s present is %t, want %t", name, got, want)
	}
}

func (f *orphanFixture) waitUntil(condition func() bool, description string) {
	f.t.Helper()

	deadline := time.Now().Add(orphanWaitTimeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(orphanPollInterval)
	}
	f.t.Fatalf("timed out waiting for %s", description)
}

func (f *orphanFixture) assertNoLeaks() {
	f.t.Helper()

	for _, prov := range f.ledgers {
		for _, leak := range prov.Ledger.Leaks() {
			f.t.Errorf("leaked instance: %s", leak)
		}
	}
}

type undeletingProvider struct {
	*fake.Provider
}

func (undeletingProvider) Delete(context.Context, string) error {
	return nil
}
