package controller

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

func (h *harness) retireForFailingToLaunch() {
	h.t.Helper()
	h.prov.FailCreate = func(string) error { return errors.New("fake: provider is unreachable") }
	h.settleIgnoringErrors(maxSettlePasses)
	h.clock.Advance(instanceLaunchTimeout + time.Minute)
	h.settleIgnoringErrors(maxSettlePasses)
	h.prov.FailCreate = nil
}

func TestARetiredInstanceReportsItsStallAsTheDegradedReason(t *testing.T) {
	h := newHarness(t)
	h.settle()
	h.clock.Advance(nodeRegistrationTimeout + time.Minute)
	h.settle()

	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionDegraded, reasonRegistrationTimeout, "did not register within")
}

func TestAHealthyLeaseIsNeverGivenADegradedCondition(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()

	if current := h.condition(v1alpha1.ConditionDegraded); current != nil {
		t.Errorf("a lease that never degraded reports Degraded=%s (%s)", current.Status, current.Reason)
	}
}

func TestDegradedIsClearedOnceTheReplacedCapacityIsReady(t *testing.T) {
	h := newHarness(t)
	h.retireForFailingToLaunch()
	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionTrue)

	name := h.instanceName(0)
	h.prov.Seed(provider.Instance{
		Name:   name,
		Region: testRegion,
		Size:   testSize,
		Labels: instanceLabels(h.lease()),
	})
	h.settle()
	h.joinNode(name, true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionDegraded, reasonRecovered, "no degradation observed")
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionTrue)
	if got := h.lease().Status.Phase; got != v1alpha1.LeasePhaseActive {
		t.Errorf("a recovered lease reports phase %q, want %q", got, v1alpha1.LeasePhaseActive)
	}
}

func TestARecoveredLeaseIsTornDownAsACleanRelease(t *testing.T) {
	h := newHarness(t)
	h.retireForFailingToLaunch()

	name := h.instanceName(0)
	h.prov.Seed(provider.Instance{
		Name:   name,
		Region: testRegion,
		Size:   testSize,
		Labels: instanceLabels(h.lease()),
	})
	h.settle()
	h.joinNode(name, true)
	h.settle()

	h.deleteLease()
	h.settle()

	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 1)
	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released_degraded"}, 0)
}
