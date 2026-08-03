package controller

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

const (
	testRegion        = "fake-a"
	testSize          = "fake-small"
	testLeaseDuration = time.Hour
	testWorkloadNS    = "workloads"
	maxSettlePasses   = 40
)

type stubClock struct {
	mu      sync.Mutex
	current time.Time
}

func newStubClock() *stubClock {
	return &stubClock{current: testInstant}
}

func (c *stubClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *stubClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(d)
}

type harness struct {
	t           *testing.T
	api         client.Client
	prov        *fake.Provider
	kube        *k8sfake.Clientset
	clock       *stubClock
	name        string
	providerErr error
}

func newHarness(t *testing.T, mutators ...func(*v1alpha1.CapacityLease)) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		api:   apiServerClient(t),
		clock: newStubClock(),
		name:  objectName(t),
		kube:  newKubeClient(),
	}
	h.prov = fake.NewWithClock(h.clock.Now)

	t.Cleanup(h.assertNoLeaks)
	h.createProviderConfig()
	h.createLease(mutators...)
	return h
}

func (h *harness) createProviderConfig() {
	h.t.Helper()
	cfg := &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: h.name},
		Spec: v1alpha1.ProviderConfigSpec{
			Type: v1alpha1.ProviderTypeHetzner,
			Hetzner: &v1alpha1.HetznerProviderSpec{
				CredentialsSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "hcloud"},
					Key:                  "token",
				},
				CloudInitSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cloud-init"},
					Key:                  "user-data",
				},
			},
			Watchdog: testPolicy(testRenewInterval, testSlack),
		},
	}
	if err := h.api.Create(h.t.Context(), cfg); err != nil {
		h.t.Fatalf("create providerconfig: %v", err)
	}
	h.t.Cleanup(func() { _ = h.api.Delete(context.Background(), cfg) })
}

func (h *harness) createLease(mutators ...func(*v1alpha1.CapacityLease)) {
	h.t.Helper()
	lease := &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: h.name},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: h.name,
			Region:      testRegion,
			Size:        testSize,
			Replicas:    1,
			Duration:    metav1.Duration{Duration: testLeaseDuration},
		},
	}
	for _, mutate := range mutators {
		mutate(lease)
	}
	if err := h.api.Create(h.t.Context(), lease); err != nil {
		h.t.Fatalf("create lease: %v", err)
	}
	h.t.Cleanup(h.forceRemoveLease)
}

func (h *harness) forceRemoveLease() {
	ctx := context.Background()
	lease := &v1alpha1.CapacityLease{}
	if err := h.api.Get(ctx, client.ObjectKey{Name: h.name}, lease); err != nil {
		return
	}
	if len(lease.Finalizers) > 0 {
		lease.Finalizers = nil
		_ = h.api.Update(ctx, lease)
	}
	_ = h.api.Delete(ctx, lease)
}

func (h *harness) assertNoLeaks() {
	h.t.Helper()
	for _, leak := range h.prov.Ledger.Leaks() {
		h.t.Errorf("provider ledger reports a leak: %s", leak)
	}
}

func (h *harness) reconciler() *CapacityLeaseReconciler {
	return &CapacityLeaseReconciler{
		Client: h.api,
		Kube:   h.kube,
		Clock:  h.clock.Now,
		Provider: func(context.Context, *v1alpha1.ProviderConfig) (provider.Provider, error) {
			if h.providerErr != nil {
				return nil, h.providerErr
			}
			return h.prov, nil
		},
	}
}

func (h *harness) reconcile() (ctrl.Result, error) {
	h.t.Helper()
	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: h.name}}
	return h.reconciler().Reconcile(h.t.Context(), req)
}

func (h *harness) settle() {
	h.t.Helper()
	if err := h.trySettle(); err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
}

func (h *harness) trySettle() error {
	h.t.Helper()
	for range maxSettlePasses {
		res, err := h.reconcile()
		if err != nil {
			return err
		}
		if res.RequeueAfter != stepRequeue {
			return nil
		}
	}
	h.t.Fatalf("lease did not settle within %d passes", maxSettlePasses)
	return nil
}

func (h *harness) settleIgnoringErrors(passes int) {
	h.t.Helper()
	for range passes {
		_, _ = h.reconcile()
	}
}

func (h *harness) lease() *v1alpha1.CapacityLease {
	h.t.Helper()
	lease := &v1alpha1.CapacityLease{}
	if err := h.api.Get(h.t.Context(), client.ObjectKey{Name: h.name}, lease); err != nil {
		h.t.Fatalf("get lease: %v", err)
	}
	return lease
}

func (h *harness) leaseGone() bool {
	h.t.Helper()
	lease := &v1alpha1.CapacityLease{}
	err := h.api.Get(h.t.Context(), client.ObjectKey{Name: h.name}, lease)
	return err != nil
}

func (h *harness) deleteLease() {
	h.t.Helper()
	if err := h.api.Delete(h.t.Context(), h.lease()); err != nil {
		h.t.Fatalf("delete lease: %v", err)
	}
}

func (h *harness) instanceName(ordinal int) string {
	return instanceName(h.lease(), ordinal)
}

func (h *harness) providerInstances() []provider.Instance {
	h.t.Helper()
	instances, err := h.prov.List(h.t.Context(), nil)
	if err != nil {
		h.t.Fatalf("list provider instances: %v", err)
	}
	return instances
}

func (h *harness) assertProviderEmpty() {
	h.t.Helper()
	if instances := h.providerInstances(); len(instances) > 0 {
		h.t.Errorf("provider still holds %d instances, want none", len(instances))
	}
}

func (h *harness) joinNode(name string, ready bool) *corev1.Node {
	h.t.Helper()
	inst, err := h.prov.Get(h.t.Context(), name)
	if err != nil {
		h.t.Fatalf("get instance %q: %v", name, err)
	}
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			UID:    types.UID(name),
			Labels: map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue},
		},
		Spec: corev1.NodeSpec{ProviderID: inst.ProviderID},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
	created, err := h.kube.CoreV1().Nodes().Create(h.t.Context(), node, metav1.CreateOptions{})
	if err != nil {
		h.t.Fatalf("create node %q: %v", name, err)
	}
	return created
}

func (h *harness) node(name string) (*corev1.Node, bool) {
	h.t.Helper()
	node, err := h.kube.CoreV1().Nodes().Get(h.t.Context(), name, metav1.GetOptions{})
	if err != nil {
		return nil, false
	}
	return node, true
}

func (h *harness) instanceStatus(name string) v1alpha1.InstanceStatus {
	h.t.Helper()
	entry := findInstance(h.lease(), name)
	if entry == nil {
		h.t.Fatalf("lease records no instance %q", name)
	}
	return *entry
}

func (h *harness) condition(name string) *metav1.Condition {
	h.t.Helper()
	return meta.FindStatusCondition(h.lease().Status.Conditions, name)
}

func (h *harness) assertCondition(condition string, want metav1.ConditionStatus) {
	h.t.Helper()
	current := h.condition(condition)
	if current == nil {
		if want != metav1.ConditionUnknown {
			h.t.Errorf("lease carries no %s condition, want %s", condition, want)
		}
		return
	}
	if current.Status != want {
		h.t.Errorf("condition %s is %s, want %s (%s: %s)", condition, current.Status, want, current.Reason, current.Message)
	}
}

func (h *harness) assertConditionDetail(condition, wantReason, wantMessage string) {
	h.t.Helper()
	current := h.condition(condition)
	if current == nil {
		h.t.Fatalf("lease carries no %s condition", condition)
	}
	if current.Reason != wantReason {
		h.t.Errorf("condition %s reports reason %q, want %q", condition, current.Reason, wantReason)
	}
	if !strings.Contains(current.Message, wantMessage) {
		h.t.Errorf("condition %s reports message %q, want it to mention %q", condition, current.Message, wantMessage)
	}
}

func (h *harness) hasFinalizer() bool {
	h.t.Helper()
	for _, f := range h.lease().Finalizers {
		if f == capacityLeaseFinalizer {
			return true
		}
	}
	return false
}

func (h *harness) seedWorkload() {
	h.t.Helper()
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testWorkloadNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{{Key: "existing", Operator: corev1.TolerationOpExists}},
				},
			},
		},
	}
	if _, err := h.kube.AppsV1().Deployments(testWorkloadNS).Create(h.t.Context(), deployment, metav1.CreateOptions{}); err != nil {
		h.t.Fatalf("create deployment: %v", err)
	}
	h.seedPod("api-0", testWorkloadNS, "home-0")
}

func (h *harness) seedPod(name, namespace, nodeName string) {
	h.t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := h.kube.CoreV1().Pods(namespace).Create(h.t.Context(), pod, metav1.CreateOptions{}); err != nil {
		h.t.Fatalf("create pod %s/%s: %v", namespace, name, err)
	}
}

func (h *harness) podExists(name, namespace string) bool {
	h.t.Helper()
	_, err := h.kube.CoreV1().Pods(namespace).Get(h.t.Context(), name, metav1.GetOptions{})
	return err == nil
}

func (h *harness) deploymentAnnotations() map[string]string {
	h.t.Helper()
	deployment, err := h.kube.AppsV1().Deployments(testWorkloadNS).Get(h.t.Context(), "api", metav1.GetOptions{})
	if err != nil {
		h.t.Fatalf("get deployment: %v", err)
	}
	return deployment.Annotations
}

type statusWriteFailure struct {
	client.Client
	err error
}

func (c statusWriteFailure) Status() client.SubResourceWriter {
	return failingSubResourceWriter{err: c.err}
}

type failingSubResourceWriter struct {
	client.SubResourceWriter
	err error
}

func (w failingSubResourceWriter) Update(context.Context, client.Object, ...client.SubResourceUpdateOption) error {
	return w.err
}

func newKubeClient(objects ...runtime.Object) *k8sfake.Clientset {
	kc := k8sfake.NewSimpleClientset(objects...)
	kc.Resources = append(kc.Resources, &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{
			Name:    "pods/eviction",
			Kind:    "Eviction",
			Group:   "policy",
			Version: "v1",
		}},
	})
	kc.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		create, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		if eviction, ok := create.GetObject().(interface{ GetName() string }); ok {
			_ = kc.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), action.GetNamespace(), eviction.GetName())
		}
		return true, nil, nil
	})
	return kc
}
