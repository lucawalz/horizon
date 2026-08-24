package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const roomyMaxLifetime = 8 * time.Hour

func (h *harness) setLeaseDuration(duration time.Duration) {
	h.t.Helper()
	lease := h.lease()
	lease.Spec.Duration = metav1.Duration{Duration: duration}
	if err := h.api.Update(h.t.Context(), lease); err != nil {
		h.t.Fatalf("set lease duration to %s: %v", duration, err)
	}
}

func (h *harness) setMaxLifetime(lifetime time.Duration) {
	h.t.Helper()
	cfg := &v1alpha1.ProviderConfig{}
	if err := h.api.Get(h.t.Context(), client.ObjectKey{Name: h.name}, cfg); err != nil {
		h.t.Fatalf("get providerconfig: %v", err)
	}
	cfg.Spec.Watchdog.MaxLifetime = metav1.Duration{Duration: lifetime}
	if err := h.api.Update(h.t.Context(), cfg); err != nil {
		h.t.Fatalf("set maxLifetime to %s: %v", lifetime, err)
	}
}

func (h *harness) deadline() time.Time {
	h.t.Helper()
	expires := h.lease().Status.ExpiresAt
	if expires == nil {
		h.t.Fatal("lease carries no deadline")
	}
	return expires.Time
}

func TestExtendingTheDurationMovesTheDeadlineByTheSameDelta(t *testing.T) {
	const extension = time.Hour
	h := newHarness(t)
	h.setMaxLifetime(roomyMaxLifetime)
	h.becomeReady()
	before := h.deadline()

	h.setLeaseDuration(testLeaseDuration + extension)
	h.settle()

	if want := before.Add(extension); !h.deadline().Equal(want) {
		t.Errorf("deadline is %s after extending the duration by %s, want %s", h.deadline(), extension, want)
	}
	h.assertCondition(v1alpha1.ConditionExpiryClamped, metav1.ConditionFalse)
}

func TestADeadlineBeyondTheNodeBackstopIsHeldAtTheBackstop(t *testing.T) {
	const shortMaxLifetime = 10 * time.Minute
	h := newHarness(t)
	h.becomeReady()

	h.setMaxLifetime(shortMaxLifetime)
	h.settle()

	created := h.instanceStatus(h.instanceName(0)).CreatedAt
	if created == nil {
		t.Fatal("the instance carries no creation instant to anchor the backstop on")
	}
	if want := created.Time.Add(shortMaxLifetime); !h.deadline().Equal(want) {
		t.Errorf("deadline is %s, want it held at the node backstop %s", h.deadline(), want)
	}
	h.assertCondition(v1alpha1.ConditionExpiryClamped, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionExpiryClamped, reasonNodeLifetimeBackstop, "maxLifetime")
}

func TestShrinkingTheDurationBelowElapsedTimeExpiresTheLeaseWithoutDraining(t *testing.T) {
	const (
		shortened = 10 * time.Minute
		elapsed   = 30 * time.Minute
	)
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: time.Minute}
	})
	h.setMaxLifetime(roomyMaxLifetime)
	name := h.becomeReady()
	h.seedPod("straggler", testWorkloadNS, name)

	h.clock.Advance(elapsed)
	h.setLeaseDuration(shortened)
	h.settle()

	h.assertCondition(v1alpha1.ConditionExpired, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
	h.assertProviderEmpty()
	if _, ok := h.node(name); ok {
		t.Errorf("node %q outlived a deadline the operator moved into the past", name)
	}
	if !h.podExists("straggler", testWorkloadNS) {
		t.Error("a deadline already spent still drained the node instead of releasing at once")
	}
}

func TestALeaseWhoseDurationNeverChangesKeepsItsAcceptedDeadline(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()

	accepted := h.lease().Status.AcceptedAt
	if accepted == nil {
		t.Fatal("the lease was never accepted")
	}
	deadline := h.deadline()
	if want := accepted.Time.Add(testLeaseDuration); !deadline.Equal(want) {
		t.Fatalf("deadline is %s, want acceptance plus the requested duration %s", deadline, want)
	}
	h.assertCondition(v1alpha1.ConditionExpiryClamped, metav1.ConditionFalse)

	h.clock.Advance(testLeaseDuration / 2)
	h.settle()

	if got := h.deadline(); !got.Equal(deadline) {
		t.Errorf("the deadline moved to %s on a later pass, want it to stay at %s", got, deadline)
	}

	h.clock.Advance(testLeaseDuration)
	h.settle()

	h.assertCondition(v1alpha1.ConditionExpired, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionReleased, metav1.ConditionTrue)
	h.assertProviderEmpty()
}

func leaseHolding(instances ...v1alpha1.InstanceStatus) *v1alpha1.CapacityLease {
	lease := &v1alpha1.CapacityLease{}
	lease.Status.Instances = instances
	return lease
}

func instanceCreatedAt(phase v1alpha1.InstancePhase, created *metav1.Time) v1alpha1.InstanceStatus {
	return v1alpha1.InstanceStatus{Name: "one", Phase: phase, CreatedAt: created}
}

func TestTheBackstopFollowsTheEarliestInstanceStillHeld(t *testing.T) {
	const maxLifetime = time.Hour
	early := &metav1.Time{Time: testInstant}
	late := &metav1.Time{Time: testInstant.Add(20 * time.Minute)}

	cases := []struct {
		name  string
		lease *v1alpha1.CapacityLease
		want  *metav1.Time
	}{
		{name: "no instance has been created yet", lease: leaseHolding()},
		{
			name:  "the earliest of several held instances anchors it",
			lease: leaseHolding(instanceCreatedAt(v1alpha1.InstancePhaseJoined, late), instanceCreatedAt(v1alpha1.InstancePhaseCreated, early)),
			want:  early,
		},
		{
			name:  "a released instance no longer anchors it",
			lease: leaseHolding(instanceCreatedAt(v1alpha1.InstancePhaseReleased, early), instanceCreatedAt(v1alpha1.InstancePhaseCreated, late)),
			want:  late,
		},
		{
			name:  "an instance with no creation instant anchors nothing",
			lease: leaseHolding(instanceCreatedAt(v1alpha1.InstancePhaseCreated, nil)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy(testRenewInterval, testSlack)
			policy.MaxLifetime = metav1.Duration{Duration: maxLifetime}

			got, bounded := nodeLifetimeBackstop(tc.lease, policy)

			if bounded != (tc.want != nil) {
				t.Fatalf("backstop reports bounded=%v, want %v", bounded, tc.want != nil)
			}
			if bounded && !got.Equal(tc.want.Time.Add(maxLifetime)) {
				t.Errorf("backstop is %s, want %s", got, tc.want.Time.Add(maxLifetime))
			}
		})
	}
}
