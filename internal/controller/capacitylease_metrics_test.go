package controller

import (
	"errors"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	pinnedSelection = "pinned"
	overtime        = 12 * time.Second
	lifetimeSeconds = float64(testLeaseDuration+overtime) / float64(time.Second)
)

func (h *harness) becomeReady() string {
	h.t.Helper()
	h.settle()
	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	return name
}

func (h *harness) burstAndExpire() {
	h.t.Helper()
	h.becomeReady()
	h.clock.Advance(testLeaseDuration + overtime)
	h.settle()
}

func TestLeaseReadySecondsIsObservedOnceWhenTheLastNodeJoins(t *testing.T) {
	h := newHarness(t)
	h.settle()
	h.clock.Advance(71 * time.Second)
	h.joinNode(h.instanceName(0), true)
	h.settle()
	h.settle()

	h.assertObservations(leaseReadySecondsMetric,
		map[string]string{"instance_type": testSize, "selection": pinnedSelection}, 1, 71)
}

func TestLeaseReadySecondsStaysEmptyWhileNoNodeHasJoined(t *testing.T) {
	h := newHarness(t)
	h.settle()

	h.assertObservations(leaseReadySecondsMetric,
		map[string]string{"instance_type": testSize, "selection": pinnedSelection}, 0, 0)
}

func TestLeaseReleaseSecondsMeasuresFromTheDeadlineToTheLastInstanceGoing(t *testing.T) {
	h := newHarness(t)
	h.burstAndExpire()
	h.settle()

	h.assertObservations(leaseReleaseSecondsMetric,
		map[string]string{"instance_type": testSize}, 1, overtime.Seconds())
}

func TestLeaseReleaseSecondsIgnoresALeaseThatNeverHeldCapacity(t *testing.T) {
	h := newHarness(t)
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("finalizer pass: %v", err)
	}
	h.deleteLease()
	h.settle()

	if !h.leaseGone() {
		t.Fatal("the lease was not released")
	}
	if series := gatheredSeries(t, leaseReleaseSecondsMetric, map[string]string{"provider": h.name}); series != nil {
		t.Errorf("a lease that never held capacity observed a release: %v", series)
	}
	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 0)
}

func TestACleanTeardownCountsOneReleasedOutcome(t *testing.T) {
	h := newHarness(t)
	h.burstAndExpire()
	h.settle()

	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 1)
	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released_degraded"}, 0)
}

func TestATeardownThatSkippedAStepCountsADegradedOutcome(t *testing.T) {
	h := newHarness(t)
	name := h.becomeReady()

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

	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released_degraded"}, 1)
	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 0)
}

func TestAControllerReleaseMovesTheWholeCostFamilyTogether(t *testing.T) {
	h := newHarness(t)
	h.burstAndExpire()

	instanceType := map[string]string{"instance_type": testSize}
	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "controller"}, 1)
	h.assertCounter(instanceSecondsMetric, instanceType, lifetimeSeconds)
	h.assertCounter(instanceBilledHoursMetric, instanceType, 2)
	h.assertCounter(instanceUndatedMetric, instanceType, 0)
}

func TestAReleaseWithNoRecordedCreationInstantIsCountedWithoutACost(t *testing.T) {
	h := newHarness(t)
	h.settle()

	lease := h.lease()
	lease.Status.Instances[0].CreatedAt = nil
	if err := h.api.Status().Update(t.Context(), lease); err != nil {
		t.Fatalf("drop the recorded creation instant: %v", err)
	}

	h.deleteLease()
	h.settle()

	instanceType := map[string]string{"instance_type": testSize}
	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "controller"}, 1)
	h.assertCounter(instanceUndatedMetric, instanceType, 1)
	h.assertCounter(instanceSecondsMetric, instanceType, 0)
	h.assertCounter(instanceBilledHoursMetric, instanceType, 0)
}

func (h *harness) vanishAfter(elapsed time.Duration) {
	h.t.Helper()
	name := h.becomeReady()
	h.clock.Advance(elapsed)
	if err := h.prov.Delete(h.t.Context(), name); err != nil {
		h.t.Fatalf("delete instance %q behind the controller: %v", name, err)
	}
	if _, err := h.reconcile(); err != nil {
		h.t.Fatalf("reconcile after the instance vanished: %v", err)
	}
}

func TestAnInstanceThatVanishesPastTheWatchdogDeadlineIsAttributedToTheNode(t *testing.T) {
	h := newHarness(t)
	h.vanishAfter(testRenewInterval + testSlack + time.Minute)

	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "node"}, 1)
	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "external"}, 0)
}

func TestAnInstanceThatVanishesLongBeforeTheWatchdogDeadlineIsAttributedElsewhere(t *testing.T) {
	h := newHarness(t)
	h.vanishAfter(time.Second)

	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "external"}, 1)
	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "node"}, 0)
}

func TestALeaseReleasedTwiceIsCountedOnce(t *testing.T) {
	h := newHarness(t)
	h.burstAndExpire()
	h.clock.Advance(time.Hour)
	h.settle()

	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 1)
	h.assertObservations(leaseReleaseSecondsMetric,
		map[string]string{"instance_type": testSize}, 1, overtime.Seconds())
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
}

func TestAnInstanceThatNeverReachedTheProviderIsNotCountedAsReleased(t *testing.T) {
	h := newHarness(t)
	h.prov.FailCreate = func(string) error { return errors.New("fake: out of capacity") }
	h.settleIgnoringErrors(maxSettlePasses)

	if got := h.instanceStatus(h.instanceName(0)).Phase; got != v1alpha1.InstancePhaseIntended {
		t.Fatalf("instance phase is %q, want %q", got, v1alpha1.InstancePhaseIntended)
	}

	h.prov.FailCreate = nil
	h.deleteLease()
	h.settle()

	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "controller"}, 0)
	h.assertCounter(instanceUndatedMetric, map[string]string{"instance_type": testSize}, 0)
	h.assertObservations(leaseReleaseSecondsMetric, map[string]string{"instance_type": testSize}, 0, 0)
	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 1)
}

func TestARetriedNodeDeletionDoesNotCountTheReleaseTwice(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()

	refusals := 0
	h.kube.PrependReactor("delete", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		refusals++
		return true, nil, errors.New("fake: the apiserver is unreachable")
	})

	h.deleteLease()
	h.settleIgnoringErrors(3)
	if refusals < 2 {
		t.Fatalf("the node deletion was attempted %d times, want at least 2", refusals)
	}

	released := map[string]string{"instance_type": testSize, "path": "controller"}
	h.assertCounter(instanceReleasedMetric, released, 0)

	h.kube.ReactionChain = h.kube.ReactionChain[1:]
	h.settle()

	h.assertCounter(instanceReleasedMetric, released, 1)
	h.assertProviderEmpty()
}

func TestARefusedStatusWriteDoesNotCountTheTeardownTwice(t *testing.T) {
	for _, refuseWrite := range []int{2, 3} {
		t.Run(fmt.Sprintf("write-%d", refuseWrite), func(t *testing.T) {
			h := newHarness(t)
			h.becomeReady()
			h.clock.Advance(testLeaseDuration + overtime)

			refuser := &refusingStatusWriter{
				refuseWrite: refuseWrite,
				err:         errors.New("fake: the apiserver refused the status write"),
			}
			h.wrapAPI = func(api client.Client) client.Client {
				refuser.Client = api
				return refuser
			}
			h.settleIgnoringErrors(maxSettlePasses)
			if refuser.writes < refuseWrite {
				t.Fatalf("teardown made %d status writes, want at least %d", refuser.writes, refuseWrite)
			}

			h.wrapAPI = nil
			h.settle()

			instanceType := map[string]string{"instance_type": testSize}
			h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "controller"}, 1)
			h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 1)
			h.assertObservations(leaseReleaseSecondsMetric, instanceType, 1, overtime.Seconds())
			h.assertCounter(instanceSecondsMetric, instanceType, lifetimeSeconds)
		})
	}
}

func TestARefusedStatusWriteDoesNotCountTheReadyLeaseTwice(t *testing.T) {
	h := newHarness(t)
	h.settle()
	h.clock.Advance(71 * time.Second)
	h.joinNode(h.instanceName(0), true)

	refuser := &refusingStatusWriter{
		refuseWrite: 1,
		err:         errors.New("fake: the apiserver refused the status write"),
	}
	h.wrapAPI = func(api client.Client) client.Client {
		refuser.Client = api
		return refuser
	}
	h.settleIgnoringErrors(1)

	h.wrapAPI = nil
	h.settle()
	h.settle()

	h.assertObservations(leaseReadySecondsMetric,
		map[string]string{"instance_type": testSize, "selection": pinnedSelection}, 1, 71)
}
