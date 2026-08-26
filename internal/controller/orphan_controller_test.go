package controller

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/lucawalz/horizon/internal/provider"
)

func watchedNode(ready corev1.ConditionStatus, leaseUID string, annotations map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "burst-0",
			Labels:      map[string]string{LeaseUIDLabelKey: leaseUID},
			Annotations: annotations,
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}},
		},
	}
}

func TestTheNodeWatchIgnoresRenewalsAndKeepsEveryStrandingSignal(t *testing.T) {
	deadline := map[string]string{provider.WatchdogDeadlineAnnotationKey: "1785931200"}
	renewed := map[string]string{provider.WatchdogDeadlineAnnotationKey: "1785931380"}

	tests := map[string]struct {
		before *corev1.Node
		after  *corev1.Node
		want   bool
	}{
		"a renewed deadline is not a stranding signal": {
			before: watchedNode(corev1.ConditionTrue, "uid", deadline),
			after:  watchedNode(corev1.ConditionTrue, "uid", renewed),
		},
		"an unchanged node is not a stranding signal": {
			before: watchedNode(corev1.ConditionTrue, "uid", deadline),
			after:  watchedNode(corev1.ConditionTrue, "uid", deadline),
		},
		"a node falling out of readiness wakes the collector": {
			before: watchedNode(corev1.ConditionTrue, "uid", deadline),
			after:  watchedNode(corev1.ConditionFalse, "uid", renewed),
			want:   true,
		},
		"a node losing its lease label wakes the collector": {
			before: watchedNode(corev1.ConditionFalse, "uid", deadline),
			after:  watchedNode(corev1.ConditionFalse, "", deadline),
			want:   true,
		},
		"a node adopted by another lease wakes the collector": {
			before: watchedNode(corev1.ConditionFalse, "uid", deadline),
			after:  watchedNode(corev1.ConditionFalse, "other-uid", deadline),
			want:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := orphanNodeSignals().Update(event.UpdateEvent{ObjectOld: tc.before, ObjectNew: tc.after})
			if got != tc.want {
				t.Errorf("update passed the predicate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestJoiningNodeIsNotDeletedForBeingNotReady(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("live")
	node := f.createNode("joining", string(lease.UID), corev1.ConditionFalse)
	f.createInstance(node.Name, string(lease.UID), f.instant.Add(time.Hour))

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	f.assertInstancePresent(node.Name, true)
	if result.RequeueAfter != orphanSweepInterval {
		t.Errorf("requeue after %s, want %s", result.RequeueAfter, orphanSweepInterval)
	}
	f.assertNoLeaks()
}

func TestStrandedNodeIsDeletedWhenLeaseAndInstanceAreGone(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	node := f.createNode("stranded", string(lease.UID), corev1.ConditionFalse)
	f.createInstance(node.Name, string(lease.UID), f.instant.Add(time.Hour))
	f.deleteInstance(node.Name)
	f.deleteLease(lease)

	f.reconcileNode(node)

	f.assertNodePresent(node, false)
	f.assertNoLeaks()
}

func TestNodeIsRetainedWhileItsInstanceStillExists(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	node := f.createNode("live-instance", string(lease.UID), corev1.ConditionFalse)
	f.createInstance(node.Name, string(lease.UID), f.instant.Add(time.Hour))
	f.deleteLease(lease)

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	if result.RequeueAfter != orphanSweepInterval {
		t.Errorf("requeue after %s, want %s", result.RequeueAfter, orphanSweepInterval)
	}
	f.assertNoLeaks()
}

func TestReadyNodeIsRetainedEvenWithoutALeaseOrAnInstance(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	node := f.createNode("ready", string(lease.UID), corev1.ConditionTrue)
	f.createInstance(node.Name, string(lease.UID), f.instant.Add(time.Hour))
	f.deleteInstance(node.Name)
	f.deleteLease(lease)

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	if result.RequeueAfter != 0 {
		t.Errorf("requeue after %s, want no requeue", result.RequeueAfter)
	}
	f.assertNoLeaks()
}

func TestReRegisteredNodeIsDeletedOnceItsInstanceIsGone(t *testing.T) {
	f := newOrphanFixture(t)
	node := f.createNode("re-registered", "", corev1.ConditionFalse)

	f.reconcileNode(node)

	f.assertNodePresent(node, false)
	f.assertNoLeaks()
}

func TestJoiningNodeIsRetainedBeforeItsLeaseAdoptsIt(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("live")
	node := f.createNode("registering", "", corev1.ConditionFalse)
	f.createInstance(node.Name, string(lease.UID), f.instant.Add(time.Hour))

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	f.assertInstancePresent(node.Name, true)
	if result.RequeueAfter != orphanSweepInterval {
		t.Errorf("requeue after %s, want %s", result.RequeueAfter, orphanSweepInterval)
	}
	f.assertNoLeaks()
}

func TestReadyNodeWithoutALeaseLabelIsRetained(t *testing.T) {
	f := newOrphanFixture(t)
	node := f.createNode("ready-unlabelled", "", corev1.ConditionTrue)

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	if result.RequeueAfter != 0 {
		t.Errorf("requeue after %s, want no requeue", result.RequeueAfter)
	}
	if calls := f.provider.GetCalls(); len(calls) != 0 {
		t.Errorf("provider consulted for a ready node: %v", calls)
	}
	f.assertNoLeaks()
}

func TestExpiredInstanceWithoutALiveLeaseIsSwept(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("expired", string(lease.UID), f.instant.Add(-orphanExpiryGrace-time.Minute))
	f.deleteLease(lease)

	f.mustSweep()

	f.assertInstancePresent("expired", false)
	f.assertNoLeaks()
}

func TestExpiredInstanceWithinTheGraceIsRetained(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("just-expired", string(lease.UID), f.instant.Add(-time.Minute))
	f.deleteLease(lease)

	f.mustSweep()
	f.assertInstancePresent("just-expired", true)

	f.instant = f.instant.Add(orphanExpiryGrace)
	f.mustSweep()

	f.assertInstancePresent("just-expired", false)
	f.assertNoLeaks()
}

func TestExpiredInstanceWithALiveLeaseIsLeftAlone(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("live")
	f.createInstance("claimed", string(lease.UID), f.instant.Add(-time.Hour))

	f.mustSweep()

	f.assertInstancePresent("claimed", true)
	if calls := f.provider.DeleteCalls(); len(calls) != 0 {
		t.Errorf("deleted claimed capacity: %v", calls)
	}

	f.deleteInstance("claimed")
	f.assertNoLeaks()
}

func TestForceDeletedLeaseHasItsInstanceSwept(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("forced")
	f.addFinalizer(lease)
	f.createInstance("abandoned", string(lease.UID), f.instant.Add(-time.Hour))

	f.deleteLease(lease)
	f.mustSweep()
	f.assertInstancePresent("abandoned", true)

	f.removeFinalizer(lease)
	f.mustSweep()

	f.assertInstancePresent("abandoned", false)
	f.assertNoLeaks()
}

func TestInstanceWithoutADeadlineIsSwept(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("undeadlined", string(lease.UID), time.Time{})
	f.deleteLease(lease)

	f.mustSweep()

	f.assertInstancePresent("undeadlined", false)
	f.assertNoLeaks()
}

func TestSweepIgnoresInstancesNoLeaseEverOwned(t *testing.T) {
	f := newOrphanFixture(t)
	f.provider.Seed(instanceOutsideAnyLease("hand-rolled"))

	f.mustSweep()

	f.assertInstancePresent("hand-rolled", true)
	f.assertNoLeaks()
}

func TestSweepRetriesAfterADeleteFailure(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("stubborn", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)
	f.provider.FailDelete = func(string) error { return errProviderUnavailable }

	if err := f.sweep(); err == nil {
		t.Fatal("sweep reported success while the delete failed")
	}
	f.assertInstancePresent("stubborn", true)

	f.provider.FailDelete = nil
	f.mustSweep()

	f.assertInstancePresent("stubborn", false)
	f.assertNoLeaks()
}

func TestSweepDoesNotTrustASuccessfulDeleteCall(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("surviving", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)
	f.providers[f.config] = undeletingProvider{f.provider}

	err := f.sweep()

	if err == nil {
		t.Fatal("sweep accepted a delete call as evidence of deletion")
	}
	if !strings.Contains(err.Error(), "still present after delete") {
		t.Errorf("error %q does not name the surviving instance", err)
	}

	f.deleteInstance("surviving")
	f.assertNoLeaks()
}

func TestEveryProviderConfigIsSwept(t *testing.T) {
	f := newOrphanFixture(t)
	_, second := f.addProvider("second")
	lease := f.createLease("gone")
	f.createInstance("under-primary", string(lease.UID), f.instant.Add(-time.Hour))
	f.createInstanceIn(second, "under-second", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)

	f.mustSweep()

	f.assertInstancePresent("under-primary", false)
	f.assertInstancePresentIn(second, "under-second", false)
	f.assertNoLeaks()
}

func TestSweepContinuesPastAProviderThatCannotBeBuilt(t *testing.T) {
	f := newOrphanFixture(t)
	f.createProviderConfig("broken")
	lease := f.createLease("gone")
	f.createInstance("expired", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)

	err := f.sweep()

	if err == nil {
		t.Fatal("sweep hid a provider config it could not build")
	}
	if !errors.Is(err, errProviderUnbuildable) {
		t.Errorf("error %q does not report the unbuildable provider config", err)
	}
	f.assertInstancePresent("expired", false)
	f.assertNoLeaks()
}

func TestNodeIsRetainedWhileAnotherProviderStillHoldsItsInstance(t *testing.T) {
	f := newOrphanFixture(t)
	_, second := f.addProvider("second")
	lease := f.createLease("gone")
	node := f.createNode("elsewhere", string(lease.UID), corev1.ConditionFalse)
	f.createInstanceIn(second, node.Name, string(lease.UID), f.instant.Add(time.Hour))
	f.deleteLease(lease)

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	if result.RequeueAfter != orphanSweepInterval {
		t.Errorf("requeue after %s, want %s", result.RequeueAfter, orphanSweepInterval)
	}
	f.assertNoLeaks()
}

func TestNodeIsRetainedWhileAProviderCannotBeBuilt(t *testing.T) {
	f := newOrphanFixture(t)
	f.createProviderConfig("broken")
	lease := f.createLease("gone")
	node := f.createNode("unverifiable", string(lease.UID), corev1.ConditionFalse)
	f.deleteLease(lease)

	_, err := f.tryReconcileNode(node)

	if err == nil {
		t.Fatal("node deleted without absence proven against every provider config")
	}
	f.assertNodePresent(node, true)
	f.assertNoLeaks()
}

func TestStartSweepsBeforeWaitingForItsFirstTick(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("expired", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- f.reconciler.Start(ctx) }()

	f.waitUntil(func() bool { return !f.instanceExists("expired") }, "the initial sweep")
	cancel()
	if err := <-stopped; err != nil {
		t.Fatalf("start: %v", err)
	}

	f.assertNoLeaks()
}

func orphanLabels(config string, extra map[string]string) map[string]string {
	labels := map[string]string{"provider": config, "region": orphanTestRegion, "instance_type": orphanTestSize}
	maps.Copy(labels, extra)
	return labels
}

func TestSweepingAnExpiredInstanceRecordsItsReleaseAndItsCost(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("swept", string(lease.UID), f.instant.Add(-orphanExpiryGrace-time.Minute))
	f.deleteLease(lease)
	f.instant = f.instant.Add(10 * time.Minute)

	f.mustSweep()

	swept := orphanLabels(f.config, map[string]string{"path": "orphan"})
	if got := f.counter(instanceReleasedMetric, swept); got != 1 {
		t.Errorf("the orphan sweep recorded %v releases, want 1", got)
	}
	cost := orphanLabels(f.config, nil)
	if got := f.counter(instanceSecondsMetric, cost); got != 600 {
		t.Errorf("the swept instance booked %v seconds, want 600", got)
	}
	if got := f.counter(instanceBilledHoursMetric, cost); got != 1 {
		t.Errorf("the swept instance booked %v billed hours, want 1", got)
	}
	if got := f.counter(instanceUndatedMetric, cost); got != 0 {
		t.Errorf("the swept instance was booked as undated %v times, want 0", got)
	}
	f.assertNoLeaks()
}

func TestSweepRecordsNoReleaseWhileTheInstanceSurvives(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("stubborn", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)
	f.provider.FailDelete = func(string) error { return errProviderUnavailable }

	if err := f.sweep(); err == nil {
		t.Fatal("sweep reported success while the delete failed")
	}

	swept := orphanLabels(f.config, map[string]string{"path": "orphan"})
	if got := f.counter(instanceReleasedMetric, swept); got != 0 {
		t.Errorf("a failed sweep recorded %v releases, want 0", got)
	}

	f.provider.FailDelete = nil
	f.mustSweep()
	f.assertNoLeaks()
}

func TestAHealthyLeasedNodeIsNotWokenAgain(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("healthy")
	node := f.createNode("healthy", string(lease.UID), corev1.ConditionTrue)
	f.createInstance(node.Name, string(lease.UID), f.instant.Add(time.Hour))

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	if result.RequeueAfter != 0 {
		t.Errorf("requeue after %s, want no requeue", result.RequeueAfter)
	}
	if calls := f.provider.GetCalls(); len(calls) != 0 {
		t.Errorf("provider consulted for a ready node: %v", calls)
	}
	f.assertNoLeaks()
}
