package controller

import (
	"errors"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func TestReadyAtSurvivesTeardown(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	readyAt := h.lease().Status.ReadyAt
	if readyAt == nil {
		t.Fatal("readyAt was not set before teardown")
	}

	h.clock.Advance(testLeaseDuration + time.Minute)
	h.settle()

	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionFalse)
	got := h.lease().Status.ReadyAt
	if got == nil {
		t.Fatal("readyAt was cleared by teardown")
	}
	if !got.Time.Equal(readyAt.Time) {
		t.Errorf("readyAt is %s after teardown, want it to remain %s", got.Time, readyAt.Time)
	}
}

func TestAReleasedInstanceDropsItsStageAndKeepsItsIdentity(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.armNode(name, h.clock.Now())
	h.settle()

	before := h.instanceStatus(name)
	if before.Stage != v1alpha1.InstanceStageReady {
		t.Fatalf("instance stage is %q before teardown, want %q", before.Stage, v1alpha1.InstanceStageReady)
	}
	if before.CreatedAt == nil {
		t.Fatal("instance carries no createdAt before teardown")
	}

	h.clock.Advance(testLeaseDuration + time.Minute)
	h.settle()

	got := h.instanceStatus(name)
	if got.Stage != "" {
		t.Errorf("a released instance reports stage %q, want none", got.Stage)
	}
	if got.Phase != v1alpha1.InstancePhaseReleased {
		t.Errorf("instance phase is %q, want %q", got.Phase, v1alpha1.InstancePhaseReleased)
	}
	if got.Name != before.Name {
		t.Errorf("instance name is %q after release, want %q", got.Name, before.Name)
	}
	if got.ProviderID != before.ProviderID {
		t.Errorf("instance provider id is %q after release, want %q", got.ProviderID, before.ProviderID)
	}
	if got.CreatedAt == nil || !got.CreatedAt.Time.Equal(before.CreatedAt.Time) {
		t.Errorf("instance createdAt is %v after release, want %s", got.CreatedAt, before.CreatedAt.Time)
	}
}

func TestReleaseDisarmsTheWatchdogConditionAndReportsWhy(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.armNode(name, h.clock.Now())
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue)

	h.clock.Advance(testLeaseDuration + time.Minute)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionWatchdogArmed, reasonReleased, "no joined node remains")
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionReleased, reasonReleased, "every instance is confirmed absent")
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionInstancesReady, reasonReleased, "capacity released")
}

func TestADrainingInstanceKeepsItsStageAndItsArmedWatchdog(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.armNode(name, h.clock.Now())
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue)

	h.prov.FailDelete = func(string) error { return errors.New("provider refuses to delete") }
	h.deleteLease()
	h.settleIgnoringErrors(3)

	got := h.instanceStatus(name)
	if got.Phase != v1alpha1.InstancePhaseDraining {
		t.Fatalf("instance phase is %q, want %q", got.Phase, v1alpha1.InstancePhaseDraining)
	}
	if got.Stage != v1alpha1.InstanceStageReady {
		t.Errorf("a draining instance reports stage %q, want %q", got.Stage, v1alpha1.InstanceStageReady)
	}
	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionFalse)
}

func TestReleasedAtIsSetExactlyOnceWhenReleased(t *testing.T) {
	h := newHarness(t)
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.clock.Advance(testLeaseDuration + time.Minute)
	h.settle()

	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
	releasedAt := h.lease().Status.ReleasedAt
	if releasedAt == nil {
		t.Fatal("releasedAt was not set when the lease reached Released")
	}
	if !releasedAt.Time.Equal(h.clock.Now()) {
		t.Errorf("releasedAt is %s, want the current time %s", releasedAt.Time, h.clock.Now())
	}

	h.clock.Advance(time.Minute)
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("reconcile after release: %v", err)
	}
	if got := h.lease().Status.ReleasedAt; !got.Time.Equal(releasedAt.Time) {
		t.Errorf("releasedAt moved to %s on a later pass, want it to stay at %s", got.Time, releasedAt.Time)
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
	h.clock.Advance(30 * time.Second)
	h.settle()

	h.assertProviderEmpty()
	if h.podExists("straggler", testWorkloadNS) {
		t.Error("the drain left a pod behind on a node that is going away")
	}
}

func TestTeardownWithholdsReleaseWhileTheWorkloadRemainsOnBurstNodes(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload()
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue)

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("restore pass: %v", err)
	}
	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse)
	h.seedPod("stuck", testWorkloadNS, name)

	for range 5 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("reconcile while the workload sits on the burst node: %v", err)
		}
	}

	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances, want the release withheld", got)
	}
	if _, ok := h.node(name); !ok {
		t.Error("the burst node was drained before the workload left it")
	}
}

func TestTeardownProceedsOnceTheWorkloadLeavesTheBurstNodes(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload()
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("restore pass: %v", err)
	}
	h.seedPod("api-1", testWorkloadNS, "home-1")

	h.settle()

	h.assertProviderEmpty()
	if _, ok := h.node(name); ok {
		t.Error("the burst node outlived a workload restored to a local node")
	}
	if !h.leaseGone() {
		t.Error("the lease was not released after the workload left the burst nodes")
	}
}

func TestTeardownProceedsAfterTheRestoreGraceElapsesWithTheWorkloadStillNotReady(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: time.Minute}
	})
	h.seedWorkload()
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("restore pass: %v", err)
	}
	h.seedPod("stuck", testWorkloadNS, name)

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("reconcile before the grace elapses: %v", err)
	}
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances before the grace elapses, want the release withheld", got)
	}

	h.clock.Advance(2 * time.Minute)
	h.settle()

	h.assertProviderEmpty()
	if !h.leaseGone() {
		t.Error("the lease was not released once the restore grace elapsed")
	}
}

func TestAZeroTeardownGraceSkipsTheRestoreGate(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: 0}
	})
	h.seedWorkload()
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("restore pass: %v", err)
	}
	h.seedPod("stuck", testWorkloadNS, name)

	podListsAfterRestore := 0
	h.kube.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		podListsAfterRestore++
		return false, nil, nil
	})

	h.settle()

	h.assertProviderEmpty()
	if !h.leaseGone() {
		t.Error("a zero teardown grace still waited on the workload")
	}
	if podListsAfterRestore != 0 {
		t.Errorf("a zero teardown grace still queried workload placement %d times, want the gate skipped entirely", podListsAfterRestore)
	}
}

func TestTeardownDoesNotWaitOnAWorkloadThatNeverMigrated(t *testing.T) {
	const shortLease = 5 * time.Minute
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
		lease.Spec.Duration = metav1.Duration{Duration: shortLease}
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: 10 * time.Minute}
	})
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testWorkloadNS},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{}},
	}
	if _, err := h.kube.AppsV1().Deployments(testWorkloadNS).Create(h.t.Context(), deployment, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settleIgnoringErrors(5)
	h.seedPod("stranded", testWorkloadNS, name)

	h.assertConditionDetail(v1alpha1.ConditionWorkloadMigrated, reasonMigrateFailed, "empty selector")

	h.clock.Advance(shortLease + time.Second)
	h.settle()

	h.assertProviderEmpty()
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
}

func leaseFallingDue(deletion, expiry *metav1.Time) *v1alpha1.CapacityLease {
	lease := &v1alpha1.CapacityLease{}
	lease.DeletionTimestamp = deletion
	lease.Status.ExpiresAt = expiry
	return lease
}

func TestTeardownIsAnchoredOnTheEarlierOfDeletionAndExpiry(t *testing.T) {
	early := &metav1.Time{Time: testInstant.Add(5 * time.Minute)}
	late := &metav1.Time{Time: testInstant.Add(time.Hour)}

	cases := []struct {
		name     string
		deletion *metav1.Time
		expiry   *metav1.Time
		want     *metav1.Time
	}{
		{"deleted before it expires", early, late, early},
		{"expired before it is deleted", late, early, early},
		{"deleted while it carries no deadline", early, nil, early},
		{"expired while it is not deleted", nil, early, early},
		{"neither deleted nor expired", nil, nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, due := teardownStart(leaseFallingDue(tc.deletion, tc.expiry))

			if due != (tc.want != nil) {
				t.Fatalf("teardown reports due=%v, want %v", due, tc.want != nil)
			}
			if due && !start.Equal(tc.want.Time) {
				t.Errorf("teardown is anchored at %s, want %s", start, tc.want.Time)
			}
		})
	}
}

func TestAReleaseConfirmedBeforeTeardownFellDueObservesZero(t *testing.T) {
	lease := leaseFallingDue(nil, &metav1.Time{Time: testInstant.Add(time.Hour)})
	lease.Status.ProviderConfig = objectName(t)
	lease.Status.Region = testRegion
	lease.Status.InstanceType = testSize
	lease.Status.Instances = []v1alpha1.InstanceStatus{{
		Name:       "one",
		ProviderID: "fake://1",
		Phase:      v1alpha1.InstancePhaseReleased,
	}}

	before := snapshotSeries(t)
	record := releaseDurationRecord(lease, testInstant)
	if record == nil {
		t.Fatal("a lease that held capacity recorded no release duration")
	}
	record()

	count, sum := before.observations(t, leaseReleaseSecondsMetric, map[string]string{"provider": objectName(t)})
	if count != 1 {
		t.Fatalf("%s holds %d observations, want 1", leaseReleaseSecondsMetric, count)
	}
	if sum != 0 {
		t.Errorf("a release confirmed before teardown fell due observed %v seconds, want 0", sum)
	}
}

func blockEviction(kc *k8sfake.Clientset) {
	kc.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		return true, nil, apierrors.NewTooManyRequests("pdb blocks eviction", 1)
	})
}

func TestTeardownBoundIsSharedAcrossReplicasNotMultipliedPerInstance(t *testing.T) {
	const (
		grace = 2 * time.Second
		// below one blocked eviction's real retry cost, so the test would prove nothing
		minElapsedToProveADrainActuallyBlocked = 4 * time.Second
		// short of three blocked drains each paying their own retry cost, so a shared budget is required to stay under it
		maxElapsedIfTheBudgetIsSharedNotMultiplied = 10 * time.Second
	)
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Replicas = 3
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: grace}
	})
	h.settle()
	names := [3]string{h.instanceName(0), h.instanceName(1), h.instanceName(2)}
	for _, name := range names {
		h.joinNode(name, true)
	}
	h.settle()
	for i, name := range names {
		h.seedPod(fmt.Sprintf("straggler-%d", i), testWorkloadNS, name)
	}
	blockEviction(h.kube)

	r := h.reconciler()
	r.Clock = time.Now
	h.deleteLease()

	started := time.Now()
	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: h.name}}
	if _, err := r.Reconcile(h.recordingLogs(), req); err != nil {
		t.Fatalf("teardown returned %v, want release to proceed despite blocked drains", err)
	}
	elapsed := time.Since(started)

	if elapsed < minElapsedToProveADrainActuallyBlocked {
		t.Fatalf("teardown finished in %s without any drain actually blocking, the test proves nothing", elapsed)
	}
	if elapsed >= maxElapsedIfTheBudgetIsSharedNotMultiplied {
		t.Errorf("teardown of 3 blocked drains took %s, want it bounded near one teardownGrace's real cost regardless of replica count", elapsed)
	}
	h.assertProviderEmpty()
}

func TestTheRestoreGateAndTheDrainDrawFromTheSameBudget(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: time.Minute}
	})
	h.seedWorkload()
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.clock.Advance(testLeaseDuration + time.Millisecond)
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("expire and restore pass: %v", err)
	}
	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionFalse)
	h.seedPod("stuck", testWorkloadNS, name)

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("gate poll: %v", err)
	}
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances while the gate waits, want the release withheld", got)
	}

	h.clock.Advance(time.Minute)
	if err := h.kube.CoreV1().Pods(testWorkloadNS).Delete(h.t.Context(), "stuck", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("remove stuck pod: %v", err)
	}
	h.seedPod("straggler", testWorkloadNS, name)

	h.settle()

	h.assertProviderEmpty()
	if !h.podExists("straggler", testWorkloadNS) {
		t.Error("the drain still ran after the restore gate alone spent nearly the whole teardown grace")
	}
}

func TestASpentTeardownBudgetSkipsTheDrainAndReleasesAnyway(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: time.Minute}
	})
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	h.seedPod("straggler", testWorkloadNS, name)

	h.clock.Advance(testLeaseDuration + time.Minute + time.Second)
	h.settle()

	h.assertProviderEmpty()
	if !h.podExists("straggler", testWorkloadNS) {
		t.Error("a spent teardown budget still drained the node instead of skipping straight to release")
	}
}

func TestATeardownAnchoredOnDeletionIsBoundedEvenBeforeExpiry(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: time.Minute}
	})
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	h.seedPod("straggler", testWorkloadNS, name)

	h.deleteLease()
	// the lease's hour-long expiry is still far off; only the deletion anchor can bound this
	h.clock.Advance(2 * time.Minute)
	h.settle()

	h.assertProviderEmpty()
	if !h.podExists("straggler", testWorkloadNS) {
		t.Error("a lease deleted long before its expiry still drained past its deletion-anchored deadline")
	}
}

func TestRemainingTeardownBudgetNeverExceedsGraceOrGoesNegative(t *testing.T) {
	const grace = time.Minute
	anchor := testInstant

	cases := []struct {
		name     string
		now      time.Time
		want     time.Duration
		noAnchor bool
	}{
		{name: "just became due", now: anchor, want: grace},
		{name: "partway through the grace", now: anchor.Add(20 * time.Second), want: 40 * time.Second},
		{name: "the grace has fully elapsed", now: anchor.Add(grace), want: 0},
		{name: "far past the grace", now: anchor.Add(time.Hour), want: 0},
		{name: "a stamp from another replica's clock reads ahead of now", now: anchor.Add(-time.Hour), want: grace},
		{name: "neither deletion nor expiry is due", now: anchor, want: 0, noAnchor: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lease := leaseFallingDue(nil, &metav1.Time{Time: anchor})
			if tc.noAnchor {
				lease = leaseFallingDue(nil, nil)
			}
			lease.Spec.TeardownGrace = &metav1.Duration{Duration: grace}
			r := &CapacityLeaseReconciler{Clock: func() time.Time { return tc.now }}

			if got := r.remainingTeardownBudget(lease); got != tc.want {
				t.Errorf("remaining budget is %s, want %s", got, tc.want)
			}
		})
	}
}
