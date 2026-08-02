package controller

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
)

func TestALeaseDeletedWhileProvisioningReleasesItsInstance(t *testing.T) {
	h := newHarness(t)
	h.settle()
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances before the delete, want 1", got)
	}

	h.deleteLease()
	h.settle()

	h.assertProviderEmpty()
	if !h.leaseGone() {
		t.Error("the finalizer was retained after a clean release")
	}
}

func TestTeardownResumesAfterACrashBetweenDeleteAndConfirmation(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	confirmations := 0
	h.prov.FailGet = func(string) error {
		confirmations++
		if confirmations == 1 {
			return errors.New("provider api is unavailable")
		}
		return nil
	}

	h.deleteLease()
	if _, err := h.reconcile(); err == nil {
		t.Fatal("teardown reported success while the deletion was unconfirmed")
	}
	if !h.hasFinalizer() {
		t.Error("the finalizer was dropped on an unconfirmed deletion")
	}
	if got := h.instanceStatus(name).Phase; got == v1alpha1.InstancePhaseReleased {
		t.Error("the instance was marked released without a confirming lookup")
	}

	h.settle()

	h.assertProviderEmpty()
	if _, ok := h.node(name); ok {
		t.Errorf("node %q outlived its instance", name)
	}
	if !h.leaseGone() {
		t.Error("the finalizer was retained after the release completed")
	}
}

func TestAPermanentDeleteFailureKeepsTheFinalizerAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.settle()
	h.deleteLease()
	h.prov.FailDelete = func(string) error { return errors.New("provider refuses to delete") }

	h.settleIgnoringErrors(5)

	if !h.hasFinalizer() {
		t.Error("the finalizer was dropped despite a failing delete")
	}
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionFalse)
	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionTrue)
	if got := h.lease().Status.Phase; got != v1alpha1.LeasePhaseDegraded {
		t.Errorf("lease phase is %q, want %q", got, v1alpha1.LeasePhaseDegraded)
	}
	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances, want the undeletable one to remain visible", got)
	}
}

func TestTeardownRefusesToDeleteANodeTheLeaseDoesNotOwn(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	node, ok := h.node(name)
	if !ok {
		t.Fatalf("node %q disappeared", name)
	}
	node.Labels[LeaseUIDLabelKey] = "a-different-lease"
	if _, err := h.kube.CoreV1().Nodes().Update(t.Context(), node, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("relabel node: %v", err)
	}

	h.deleteLease()
	h.settle()

	h.assertProviderEmpty()
	if _, ok := h.node(name); !ok {
		t.Error("a node the lease does not own was deleted anyway")
	}
	if !h.leaseGone() {
		t.Error("the release blocked on a node the lease must not touch")
	}
}

func TestAnExpiredLeaseReleasesItsCapacityAndKeepsItsObject(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.clock.Advance(testLeaseDuration + time.Minute)
	h.settle()

	h.assertProviderEmpty()
	h.assertCondition(v1alpha1.ConditionExpired, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
	if _, ok := h.node(name); ok {
		t.Errorf("node %q outlived the expired lease", name)
	}
	if h.leaseGone() {
		t.Fatal("the lease object was deleted on expiry")
	}
	if !h.hasFinalizer() {
		t.Error("the finalizer was dropped from a lease that still exists")
	}
	if got := h.lease().Status.Phase; got != v1alpha1.LeasePhaseReleased {
		t.Errorf("lease phase is %q, want %q", got, v1alpha1.LeasePhaseReleased)
	}
}

func TestExpiryWhileTheWorkloadIsStillMigratingRestoresPlacement(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload()
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue)

	h.clock.Advance(testLeaseDuration + time.Minute)
	h.settle()

	h.assertProviderEmpty()
	h.assertCondition(v1alpha1.ConditionExpired, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse)
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
	if _, ok := h.deploymentAnnotations()[k8s.PrePlacementAnnotationKey]; ok {
		t.Error("the saved placement annotation outlived the restore")
	}
	if got := h.lease().Status.MigratedWorkloads; len(got) != 0 {
		t.Errorf("the lease still lists migrated workloads %v after the restore", got)
	}
}

func TestADeletedLeaseIsReleasedEvenWithoutItsProviderConfig(t *testing.T) {
	h := newHarness(t)
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("finalizer pass: %v", err)
	}

	h.providerErr = errors.New("providerconfig is gone")
	h.deleteLease()
	h.settle()

	if !h.leaseGone() {
		t.Error("a lease that never reached a provider could not be deleted")
	}
}

func TestAProviderLostWhileTheLeaseExpiresDegradesAndHoldsTheFinalizer(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	revoked := errors.New("credentials secret was revoked")
	h.providerErr = revoked
	h.deleteLease()

	_, err := h.reconcile()
	if !errors.Is(err, revoked) {
		t.Fatalf("teardown returned %v, want the provider failure", err)
	}
	if !h.hasFinalizer() {
		t.Error("the finalizer was dropped while the capacity was still held")
	}
	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionDegraded, reasonProviderUnavailable, revoked.Error())
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionReleased, reasonProviderUnavailable, "capacity is still held")
	if got := h.lease().Status.Phase; got != v1alpha1.LeasePhaseDegraded {
		t.Errorf("lease phase is %q, want %q", got, v1alpha1.LeasePhaseDegraded)
	}
	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances, want the unreleased one to remain", got)
	}

	h.providerErr = nil
	h.settle()

	h.assertProviderEmpty()
	if !h.leaseGone() {
		t.Error("the lease survived a release that finally reached its provider")
	}
}

func TestDegradeKeepsTheCauseWhenTheStatusWriteAlsoFails(t *testing.T) {
	cause := errors.New("credentials secret was revoked")
	unwritable := errors.New("apiserver rejected the status update")
	lease := &v1alpha1.CapacityLease{ObjectMeta: metav1.ObjectMeta{Name: objectName(t)}}
	r := &CapacityLeaseReconciler{Client: statusWriteFailure{err: unwritable}}

	_, err := r.degrade(t.Context(), lease, reasonProviderUnavailable, cause)

	if !errors.Is(err, cause) {
		t.Errorf("returned error %v drops the provider failure", err)
	}
	if !errors.Is(err, unwritable) {
		t.Errorf("returned error %v drops the status write failure", err)
	}
}

func TestAZeroTeardownGraceSkipsTheDrainWindow(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: 0}
	})
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	h.seedPod("straggler", testWorkloadNS, name)

	h.deleteLease()
	h.settle()

	h.assertProviderEmpty()
	if !h.podExists("straggler", testWorkloadNS) {
		t.Error("a zero grace still drained the node")
	}
}

func TestADrainedNodeLosesItsPodsWithinTheGrace(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: time.Minute}
	})
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	h.seedPod("straggler", testWorkloadNS, name)

	h.deleteLease()
	h.settle()

	h.assertProviderEmpty()
	if h.podExists("straggler", testWorkloadNS) {
		t.Error("the drain left a pod behind on a node that is going away")
	}
}
