package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

const (
	// far enough out that any reconcile inside the test window came from the watch and not the requeue
	watchOnlyPollInterval = 10 * time.Minute
	watchRenewInterval    = 20 * time.Minute
	watchSlack            = 25 * time.Minute
	watchMaxLifetime      = time.Hour

	watchSettleTimeout  = 30 * time.Second
	watchSettlePoll     = 50 * time.Millisecond
	watchEventBufferLen = 64
)

type watchedLease struct {
	t    *testing.T
	kube kubernetes.Interface
	name string
}

// controller-runtime refuses a second manager naming the same controllers, so this suite starts at most one
func startWatchingManager(t *testing.T) *watchedLease {
	t.Helper()
	testEnv.SkipUnlessRunning(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(testEnv.Config, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	kube, err := kubernetes.NewForConfig(testEnv.Config)
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}

	reconciler := &CapacityLeaseReconciler{
		Client:       mgr.GetClient(),
		Kube:         kube,
		Clock:        time.Now,
		Recorder:     events.NewFakeRecorder(watchEventBufferLen),
		Catalogue:    stubCatalogue{types: []provider.InstanceType{offeredType(testSize, testRegion, true)}},
		Provider:     watchProviderFactory(fake.NewWithClock(time.Now)),
		PollInterval: watchOnlyPollInterval,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		t.Fatalf("set up the lease controller: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = mgr.Start(ctx) }()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("the manager cache never synced")
	}

	w := &watchedLease{t: t, kube: kube, name: objectName(t)}
	w.create()
	return w
}

func watchProviderFactory(prov provider.Provider) ProviderFactory {
	return func(context.Context, *v1alpha1.ProviderConfig) (provider.Provider, error) { return prov, nil }
}

func (w *watchedLease) create() {
	w.t.Helper()
	cfg := hetznerProviderConfig(w.name, v1alpha1.WatchdogPolicy{
		RenewInterval: metav1.Duration{Duration: watchRenewInterval},
		Slack:         metav1.Duration{Duration: watchSlack},
		MaxLifetime:   metav1.Duration{Duration: watchMaxLifetime},
	})
	if err := testEnv.Client.Create(w.t.Context(), cfg); err != nil {
		w.t.Fatalf("create providerconfig: %v", err)
	}
	w.t.Cleanup(func() { _ = testEnv.Client.Delete(context.Background(), cfg) })

	lease := &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: w.name},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: w.name,
			Region:      testRegion,
			Size:        testSize,
			Replicas:    1,
			Duration:    metav1.Duration{Duration: testLeaseDuration},
		},
	}
	if err := testEnv.Client.Create(w.t.Context(), lease); err != nil {
		w.t.Fatalf("create lease: %v", err)
	}
	w.t.Cleanup(w.remove)
}

func (w *watchedLease) remove() {
	ctx := context.Background()
	lease := &v1alpha1.CapacityLease{}
	if err := testEnv.Client.Get(ctx, client.ObjectKey{Name: w.name}, lease); err == nil {
		lease.Finalizers = nil
		_ = testEnv.Client.Update(ctx, lease)
		_ = testEnv.Client.Delete(ctx, lease)
	}
	_ = w.kube.CoreV1().Nodes().Delete(ctx, w.name+"-0", metav1.DeleteOptions{})
}

func (w *watchedLease) await(what string, satisfied func(*v1alpha1.CapacityLease) bool) {
	w.t.Helper()
	deadline := time.Now().Add(watchSettleTimeout)
	var last string
	for time.Now().Before(deadline) {
		lease := &v1alpha1.CapacityLease{}
		if err := testEnv.Client.Get(w.t.Context(), client.ObjectKey{Name: w.name}, lease); err == nil {
			if satisfied(lease) {
				return
			}
			if condition := conditionOf(lease, v1alpha1.ConditionInstancesReady); condition != nil {
				last = condition.Reason
			}
		}
		time.Sleep(watchSettlePoll)
	}
	w.t.Fatalf("the lease never %s within %s, last reported %q", what, watchSettleTimeout, last)
}

func conditionOf(lease *v1alpha1.CapacityLease, condition string) *metav1.Condition {
	for i := range lease.Status.Conditions {
		if lease.Status.Conditions[i].Type == condition {
			return &lease.Status.Conditions[i]
		}
	}
	return nil
}

func (w *watchedLease) registerNode(ready bool) {
	w.t.Helper()
	entry := &v1alpha1.InstanceStatus{}
	w.await("recorded a created instance", func(lease *v1alpha1.CapacityLease) bool {
		if len(lease.Status.Instances) == 0 || lease.Status.Instances[0].ProviderID == "" {
			return false
		}
		*entry = lease.Status.Instances[0]
		return true
	})

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   entry.Name,
			Labels: map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue},
		},
		Spec: corev1.NodeSpec{ProviderID: entry.ProviderID},
	}
	created, err := w.kube.CoreV1().Nodes().Create(w.t.Context(), node, metav1.CreateOptions{})
	if err != nil {
		w.t.Fatalf("register node %q: %v", node.Name, err)
	}
	w.setReady(created.Name, ready)
}

func (w *watchedLease) setReady(name string, ready bool) {
	w.t.Helper()
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := w.kube.CoreV1().Nodes().Get(w.t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		node.Status.Conditions = []corev1.NodeCondition{{
			Type:               corev1.NodeReady,
			Status:             status,
			LastTransitionTime: metav1.Now(),
		}}
		_, err = w.kube.CoreV1().Nodes().UpdateStatus(w.t.Context(), node, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		w.t.Fatalf("set node %q ready=%v: %v", name, ready, err)
	}
}

func TestANodeGoingReadyWakesTheLeaseWithoutWaitingForThePoll(t *testing.T) {
	w := startWatchingManager(t)

	w.registerNode(false)
	w.await("noticed the node registering", func(lease *v1alpha1.CapacityLease) bool {
		condition := conditionOf(lease, v1alpha1.ConditionInstancesReady)
		return condition != nil && condition.Reason == reasonAwaitingReady
	})

	w.setReady(w.name+"-0", true)
	w.await("noticed the node going ready", func(lease *v1alpha1.CapacityLease) bool {
		condition := conditionOf(lease, v1alpha1.ConditionInstancesReady)
		return condition != nil && condition.Status == metav1.ConditionTrue
	})

	lease := &v1alpha1.CapacityLease{}
	if err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: w.name}, lease); err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if lease.Status.ReadyAt == nil {
		t.Error("the lease became ready without recording a ready instant")
	}
}
