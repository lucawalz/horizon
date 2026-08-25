package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

const (
	testRegion         = "fake-a"
	testSize           = "fake-small"
	testLargeSize      = "fake-large"
	testLeaseDuration  = time.Hour
	testWorkloadNS     = "workloads"
	testWorkloadNSB    = "workloads-b"
	maxSettlePasses    = 40
	testEventBufferLen = 16
)

type stubClock struct {
	mu      sync.Mutex
	current time.Time
}

func newStubClock() *stubClock {
	return &stubClock{current: time.Now().UTC().Truncate(time.Second)}
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
	recorder    *events.FakeRecorder
	catalogue   catalogue.Reader
	baseline    seriesSnapshot
	logged      []string
	name        string
	providerErr error
	wrapAPI     func(client.Client) client.Client
}

type stubCatalogue struct {
	types []provider.InstanceType
	err   error
}

func offeredType(name, region string, available bool) provider.InstanceType {
	return provider.InstanceType{
		Name:        name,
		Region:      region,
		Available:   available,
		CPUCores:    2,
		MemoryBytes: 4 << 30,
		HourlyRate:  provider.Rate{Amount: 0.0074, Currency: "EUR"},
	}
}

func (s stubCatalogue) List(_, region string) ([]provider.InstanceType, error) {
	if s.err != nil {
		return nil, s.err
	}
	var offered []provider.InstanceType
	for _, it := range s.types {
		if it.Region == region {
			offered = append(offered, it)
		}
	}
	return offered, nil
}

func (s stubCatalogue) Age(string) (time.Duration, bool) {
	return 0, s.err == nil
}

const stubClockRealtimeTolerance = 5 * time.Minute

func assertStubClockTracksRealTime(t *testing.T, clock *stubClock) {
	t.Helper()
	if drift := time.Since(clock.Now()); drift < 0 || drift > stubClockRealtimeTolerance {
		t.Fatalf("stub clock reads %s, %s from real time: deletion-anchored teardown tests need it seeded in envtest's era, not a fixed historical constant", clock.Now(), drift)
	}
}

func newHarness(t *testing.T, mutators ...func(*v1alpha1.CapacityLease)) *harness {
	t.Helper()
	h := &harness{
		t:        t,
		api:      apiServerClient(t),
		clock:    newStubClock(),
		baseline: snapshotSeries(t),
		name:     objectName(t),
		kube:     newKubeClient(),
		recorder: events.NewFakeRecorder(testEventBufferLen),
	}
	assertStubClockTracksRealTime(t, h.clock)
	h.prov = fake.NewWithClock(h.clock.Now)
	h.catalogue = stubCatalogue{types: []provider.InstanceType{
		offeredType(testSize, testRegion, true),
		offeredType(testLargeSize, testRegion, true),
	}}

	t.Cleanup(h.assertNoLeaks)
	h.createProviderConfig(h.name)
	h.createLease(mutators...)
	return h
}

func hetznerProviderConfig(name string, policy v1alpha1.WatchdogPolicy) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
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
				NodeCredentialSecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "hcloud"},
					Key:                  "node-token",
				},
				ImageSelector: map[string]string{"caph-image-name": "bedrock-cluster-node"},
			},
			Watchdog: policy,
		},
	}
}

func (h *harness) createProviderConfig(name string) {
	h.t.Helper()
	cfg := hetznerProviderConfig(name, testPolicy(testRenewInterval, testSlack))
	if err := h.api.Create(h.t.Context(), cfg); err != nil {
		h.t.Fatalf("create providerconfig: %v", err)
	}
	h.t.Cleanup(func() { _ = h.api.Delete(context.Background(), cfg) })
}

func (h *harness) dropNodeCredential() {
	h.t.Helper()
	cfg := &v1alpha1.ProviderConfig{}
	if err := h.api.Get(h.t.Context(), client.ObjectKey{Name: h.name}, cfg); err != nil {
		h.t.Fatalf("get providerconfig: %v", err)
	}
	cfg.Spec.Hetzner.NodeCredentialSecretRef = nil
	if err := h.api.Update(h.t.Context(), cfg); err != nil {
		h.t.Fatalf("update providerconfig: %v", err)
	}
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
	api := h.api
	if h.wrapAPI != nil {
		api = h.wrapAPI(api)
	}
	return &CapacityLeaseReconciler{
		Client:    api,
		Kube:      h.kube,
		Clock:     h.clock.Now,
		Recorder:  h.recorder,
		Catalogue: h.catalogue,
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
	return h.reconciler().Reconcile(h.recordingLogs(), req)
}

func (h *harness) recordingLogs() context.Context {
	sink := funcr.New(func(_, args string) { h.logged = append(h.logged, args) }, funcr.Options{})
	return logf.IntoContext(h.t.Context(), sink)
}

func (h *harness) assertLogged(message string, fields ...string) {
	h.t.Helper()
	for _, line := range h.logged {
		if !strings.Contains(line, fmt.Sprintf("%q=%q", "msg", message)) {
			continue
		}
		for _, field := range fields {
			if !strings.Contains(line, field) {
				h.t.Errorf("the log line %s omits %s", line, field)
			}
		}
		return
	}
	h.t.Errorf("no log line reports %q, of %d recorded", message, len(h.logged))
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

func (h *harness) setNodeReady(name string, ready bool) {
	h.t.Helper()
	h.setNodeReadyAt(name, ready, time.Time{})
}

func (h *harness) setNodeReadyAt(name string, ready bool, transition time.Time) {
	h.t.Helper()
	node, ok := h.node(name)
	if !ok {
		h.t.Fatalf("node %q disappeared", name)
	}
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	node.Status.Conditions = []corev1.NodeCondition{{
		Type:               corev1.NodeReady,
		Status:             status,
		LastTransitionTime: metav1.Time{Time: transition},
	}}
	if _, err := h.kube.CoreV1().Nodes().Update(h.t.Context(), node, metav1.UpdateOptions{}); err != nil {
		h.t.Fatalf("set node %q ready=%v: %v", name, ready, err)
	}
}

func (h *harness) node(name string) (*corev1.Node, bool) {
	h.t.Helper()
	node, err := h.kube.CoreV1().Nodes().Get(h.t.Context(), name, metav1.GetOptions{})
	if err != nil {
		return nil, false
	}
	return node, true
}

func (h *harness) armNode(name string, at time.Time) {
	h.t.Helper()
	node, ok := h.node(name)
	if !ok {
		h.t.Fatalf("node %q disappeared", name)
	}
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[provider.WatchdogArmedAnnotationKey] = provider.FormatArmed(at)
	if _, err := h.kube.CoreV1().Nodes().Update(h.t.Context(), node, metav1.UpdateOptions{}); err != nil {
		h.t.Fatalf("arm node %q: %v", name, err)
	}
}

func (h *harness) events() []string {
	h.t.Helper()
	var events []string
	for {
		select {
		case event := <-h.recorder.Events:
			events = append(events, event)
		default:
			return events
		}
	}
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

func (h *harness) seedWorkload(mutators ...func(*appsv1.Deployment)) {
	h.t.Helper()
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testWorkloadNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{{Key: "existing", Operator: corev1.TolerationOpExists}},
				},
			},
		},
	}
	for _, mutate := range mutators {
		mutate(deployment)
	}
	if _, err := h.kube.AppsV1().Deployments(testWorkloadNS).Create(h.t.Context(), deployment, metav1.CreateOptions{}); err != nil {
		h.t.Fatalf("create deployment: %v", err)
	}
	h.seedPod("api-0", testWorkloadNS, "home-0")
}

func (h *harness) seedWorkloadIn(namespace, name string) {
	h.t.Helper()
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
		},
	}
	if _, err := h.kube.AppsV1().Deployments(namespace).Create(h.t.Context(), deployment, metav1.CreateOptions{}); err != nil {
		h.t.Fatalf("create deployment %s/%s: %v", namespace, name, err)
	}
}

func (h *harness) refuseWorkloadPatchesIn(namespace string) {
	h.t.Helper()
	h.kube.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() != namespace {
			return false, nil, nil
		}
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, action.GetNamespace(), errors.New("no"))
	})
}

func (h *harness) deploymentIn(namespace, name string) *appsv1.Deployment {
	h.t.Helper()
	deployment, err := h.kube.AppsV1().Deployments(namespace).Get(h.t.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.t.Fatalf("get deployment %s/%s: %v", namespace, name, err)
	}
	return deployment
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

const raceAnnotationKey = "horizon.dev/test-concurrent-writer"

type staleWriteRacer struct {
	client.Client
	key       client.ObjectKey
	raced     bool
	raceErr   error
	conflicts int
}

func (c *staleWriteRacer) Status() client.SubResourceWriter {
	return racingSubResourceWriter{SubResourceWriter: c.Client.Status(), racer: c}
}

func (c *staleWriteRacer) touchLeaseOnce(ctx context.Context) {
	if c.raced {
		return
	}
	c.raced = true
	lease := &v1alpha1.CapacityLease{}
	if err := c.Get(ctx, c.key, lease); err != nil {
		c.raceErr = err
		return
	}
	metav1.SetMetaDataAnnotation(&lease.ObjectMeta, raceAnnotationKey, "1")
	c.raceErr = c.Update(ctx, lease)
}

type racingSubResourceWriter struct {
	client.SubResourceWriter
	racer *staleWriteRacer
}

func (w racingSubResourceWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.racer.touchLeaseOnce(ctx)
	err := w.SubResourceWriter.Update(ctx, obj, opts...)
	if apierrors.IsConflict(err) {
		w.racer.conflicts++
	}
	return err
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

func (h *harness) relabelInstance(name string, mutate func(map[string]string)) {
	h.t.Helper()
	inst, err := h.prov.Get(h.t.Context(), name)
	if err != nil {
		h.t.Fatalf("get instance %q: %v", name, err)
	}
	mutate(inst.Labels)
	h.prov.Seed(inst)
}

type refusingStatusWriter struct {
	client.Client
	refuseWrite int
	writes      int
	err         error
}

func (c *refusingStatusWriter) Status() client.SubResourceWriter {
	return refusingSubResourceWriter{SubResourceWriter: c.Client.Status(), refuser: c}
}

type refusingSubResourceWriter struct {
	client.SubResourceWriter
	refuser *refusingStatusWriter
}

func (w refusingSubResourceWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.refuser.writes++
	if w.refuser.writes == w.refuser.refuseWrite {
		return w.refuser.err
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
