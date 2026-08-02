package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

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

	f.reconcileNode(node)

	f.assertNodePresent(node, true)
	f.assertNoLeaks()
}

func TestNodeWithoutALeaseLabelIsIgnored(t *testing.T) {
	f := newOrphanFixture(t)
	node := f.createNode("unlabelled", "", corev1.ConditionFalse)

	result := f.reconcileNode(node)

	f.assertNodePresent(node, true)
	if result.RequeueAfter != 0 {
		t.Errorf("requeue after %s, want no requeue", result.RequeueAfter)
	}
	if calls := f.provider.GetCalls(); len(calls) != 0 {
		t.Errorf("provider consulted for an unlabelled node: %v", calls)
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
	f.reconciler.Provider = undeletingProvider{f.provider}

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
