package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	roomyMaxLifetime = 8 * time.Hour
	// the schema floor, and the shortest edit an operator can make to a live config
	flooredMaxLifetime = 5 * time.Minute
)

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

func (h *harness) latchedBackstop(name string) time.Time {
	h.t.Helper()
	backstop := h.instanceStatus(name).BackstopAt
	if backstop == nil {
		h.t.Fatalf("instance %q latched no lifetime backstop when it was created", name)
	}
	return backstop.Time
}

func (h *harness) forgetLatchedBackstops() {
	h.t.Helper()
	lease := h.lease()
	for i := range lease.Status.Instances {
		lease.Status.Instances[i].BackstopAt = nil
	}
	if err := h.api.Status().Update(h.t.Context(), lease); err != nil {
		h.t.Fatalf("clear the latched backstops: %v", err)
	}
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
	h.setMaxLifetime(shortMaxLifetime)
	name := h.becomeReady()

	created := h.instanceStatus(name).CreatedAt
	if created == nil {
		t.Fatal("the instance carries no creation instant to anchor the backstop on")
	}
	if want := created.Time.Add(shortMaxLifetime); !h.latchedBackstop(name).Equal(want) {
		t.Errorf("the instance latched %s, want its creation plus maxLifetime %s", h.latchedBackstop(name), want)
	}
	if !h.deadline().Equal(h.latchedBackstop(name)) {
		t.Errorf("deadline is %s, want it held at the latched backstop %s", h.deadline(), h.latchedBackstop(name))
	}
	h.assertCondition(v1alpha1.ConditionExpiryClamped, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionExpiryClamped, reasonNodeLifetimeBackstop, "destroys itself")
}

func TestLoweringMaxLifetimeOnALiveConfigCannotSpendTheTeardownGrace(t *testing.T) {
	const (
		grace   = time.Minute
		elapsed = 30 * time.Minute
	)
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.TeardownGrace = &metav1.Duration{Duration: grace}
	})
	name := h.becomeReady()
	h.seedPod("straggler", testWorkloadNS, name)
	latched := h.latchedBackstop(name)
	deadline := h.deadline()

	h.clock.Advance(elapsed)
	h.setMaxLifetime(flooredMaxLifetime)
	h.settle()

	if got := h.latchedBackstop(name); !got.Equal(latched) {
		t.Errorf("the latched backstop moved to %s when the config was lowered, want it to stay at %s", got, latched)
	}
	if got := h.deadline(); !got.Equal(deadline) {
		t.Errorf("deadline moved to %s when the config was lowered, want it to stay at %s", got, deadline)
	}
	h.assertCondition(v1alpha1.ConditionExpired, metav1.ConditionUnknown)
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances after a config edit, want the lease untouched", got)
	}

	h.clock.Advance(testLeaseDuration - elapsed)
	h.settle()

	h.assertProviderEmpty()
	h.assertCondition(v1alpha1.ConditionExpired, metav1.ConditionTrue)
	if h.podExists("straggler", testWorkloadNS) {
		t.Error("the teardown grace was already spent when the lease fell due, so the drain never ran")
	}
}

func TestRaisingMaxLifetimeOnALiveConfigCannotOutrunTheMachine(t *testing.T) {
	h := newHarness(t)
	name := h.becomeReady()
	latched := h.latchedBackstop(name)

	h.setMaxLifetime(roomyMaxLifetime)
	h.setLeaseDuration(4 * time.Hour)
	h.settle()

	if got := h.latchedBackstop(name); !got.Equal(latched) {
		t.Errorf("the latched backstop moved to %s when the config was raised, want it to stay at %s", got, latched)
	}
	if got := h.deadline(); !got.Equal(latched) {
		t.Errorf("deadline is %s, want it held at the backstop %s the machine was created with", got, latched)
	}
	h.assertCondition(v1alpha1.ConditionExpiryClamped, metav1.ConditionTrue)
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
		t.Errorf("node %q outlived a deadline the lease owner moved into the past", name)
	}
	if !h.podExists("straggler", testWorkloadNS) {
		t.Error("a deadline already spent still drained the node instead of releasing at once")
	}
}

func TestALeaseWhoseDurationNeverChangesKeepsItsAcceptedDeadline(t *testing.T) {
	h := newHarness(t)
	h.setMaxLifetime(roomyMaxLifetime)
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

func TestAHeldInstanceWithNoLatchedBackstopReportsThatItCannotBeChecked(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()
	h.forgetLatchedBackstops()

	h.settle()

	h.assertCondition(v1alpha1.ConditionExpiryClamped, metav1.ConditionUnknown)
	h.assertConditionDetail(v1alpha1.ConditionExpiryClamped, reasonBackstopUnknown, "record no lifetime backstop")
}

func instanceLatching(phase v1alpha1.InstancePhase, created, backstop *metav1.Time) v1alpha1.InstanceStatus {
	return v1alpha1.InstanceStatus{Name: "one", Phase: phase, CreatedAt: created, BackstopAt: backstop}
}

func TestABackstopIsLatchedOnceAndNeverRecomputed(t *testing.T) {
	const maxLifetime = time.Hour
	created := &metav1.Time{Time: testInstant}
	latched := &metav1.Time{Time: testInstant.Add(3 * time.Hour)}

	cases := []struct {
		name  string
		entry v1alpha1.InstanceStatus
		want  *metav1.Time
	}{
		{
			name:  "a fresh instance latches its creation plus the lifetime",
			entry: instanceLatching(v1alpha1.InstancePhaseCreated, created, nil),
			want:  &metav1.Time{Time: testInstant.Add(maxLifetime)},
		},
		{
			name:  "an already latched instance keeps what it recorded",
			entry: instanceLatching(v1alpha1.InstancePhaseCreated, created, latched),
			want:  latched,
		},
		{
			name:  "an instance with no creation instant latches nothing",
			entry: instanceLatching(v1alpha1.InstancePhaseIntended, nil, nil),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy(testRenewInterval, testSlack)
			policy.MaxLifetime = metav1.Duration{Duration: maxLifetime}
			entry := tc.entry

			latchBackstop(&entry, policy)

			switch {
			case tc.want == nil && entry.BackstopAt != nil:
				t.Errorf("the instance latched %s, want nothing", entry.BackstopAt.Time)
			case tc.want != nil && entry.BackstopAt == nil:
				t.Errorf("the instance latched nothing, want %s", tc.want.Time)
			case tc.want != nil && !entry.BackstopAt.Time.Equal(tc.want.Time):
				t.Errorf("the instance latched %s, want %s", entry.BackstopAt.Time, tc.want.Time)
			}
		})
	}
}

func TestAPolicyWithNoLifetimeLatchesNoBackstop(t *testing.T) {
	entry := instanceLatching(v1alpha1.InstancePhaseCreated, &metav1.Time{Time: testInstant}, nil)

	latchBackstop(&entry, v1alpha1.WatchdogPolicy{})

	if entry.BackstopAt != nil {
		t.Errorf("a policy with no maxLifetime latched %s, want nothing", entry.BackstopAt.Time)
	}
}

func leaseHolding(instances ...v1alpha1.InstanceStatus) *v1alpha1.CapacityLease {
	lease := &v1alpha1.CapacityLease{}
	lease.Status.Instances = instances
	return lease
}

func instanceBackstopAt(phase v1alpha1.InstancePhase, backstop *metav1.Time) v1alpha1.InstanceStatus {
	return v1alpha1.InstanceStatus{Name: "one", Phase: phase, BackstopAt: backstop}
}

func TestABackstopExactlyOnTheRequestedDeadlineIsNotAClamp(t *testing.T) {
	const duration = time.Hour
	lease := leaseHolding(instanceBackstopAt(v1alpha1.InstancePhaseCreated, &metav1.Time{Time: testInstant.Add(duration)}))
	lease.Status.AcceptedAt = &metav1.Time{Time: testInstant}
	lease.Spec.Duration = metav1.Duration{Duration: duration}

	deadline := deriveDeadline(lease)

	if deadline.clamped {
		t.Error("a backstop landing exactly on the requested deadline was reported as a clamp, though nothing was reduced")
	}
	if !deadline.at.Equal(testInstant.Add(duration)) {
		t.Errorf("deadline is %s, want the requested %s", deadline.at, testInstant.Add(duration))
	}
}

func TestTheBackstopFollowsTheEarliestInstanceStillHeld(t *testing.T) {
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
			lease: leaseHolding(instanceBackstopAt(v1alpha1.InstancePhaseJoined, late), instanceBackstopAt(v1alpha1.InstancePhaseCreated, early)),
			want:  early,
		},
		{
			name:  "a released instance no longer anchors it",
			lease: leaseHolding(instanceBackstopAt(v1alpha1.InstancePhaseReleased, early), instanceBackstopAt(v1alpha1.InstancePhaseCreated, late)),
			want:  late,
		},
		{
			name:  "an instance that latched nothing anchors nothing",
			lease: leaseHolding(instanceBackstopAt(v1alpha1.InstancePhaseCreated, nil)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, bounded := nodeLifetimeBackstop(tc.lease)

			if bounded != (tc.want != nil) {
				t.Fatalf("backstop reports bounded=%v, want %v", bounded, tc.want != nil)
			}
			if bounded && !got.Equal(tc.want.Time) {
				t.Errorf("backstop is %s, want %s", got, tc.want.Time)
			}
		})
	}
}
