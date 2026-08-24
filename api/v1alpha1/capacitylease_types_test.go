package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var latchInstant = time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)

func instanceLatching(phase InstancePhase, backstop *metav1.Time) InstanceStatus {
	return InstanceStatus{Name: "one", Phase: phase, BackstopAt: backstop}
}

func statusHolding(instances ...InstanceStatus) CapacityLeaseStatus {
	return CapacityLeaseStatus{Instances: instances}
}

func TestTheLifetimeBackstopFollowsTheEarliestInstanceStillHeld(t *testing.T) {
	early := &metav1.Time{Time: latchInstant}
	late := &metav1.Time{Time: latchInstant.Add(20 * time.Minute)}

	cases := []struct {
		name   string
		status CapacityLeaseStatus
		want   *metav1.Time
	}{
		{name: "no instance has been created yet", status: statusHolding()},
		{
			name:   "the earliest of several held instances anchors it",
			status: statusHolding(instanceLatching(InstancePhaseJoined, late), instanceLatching(InstancePhaseCreated, early)),
			want:   early,
		},
		{
			name:   "a released instance no longer anchors it",
			status: statusHolding(instanceLatching(InstancePhaseReleased, early), instanceLatching(InstancePhaseCreated, late)),
			want:   late,
		},
		{
			name:   "an instance that latched nothing anchors nothing",
			status: statusHolding(instanceLatching(InstancePhaseCreated, nil)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.status.LifetimeBackstop()

			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("the backstop is %s, want none", got.Time)
			case tc.want == nil:
			case got == nil:
				t.Fatalf("the backstop is absent, want %s", tc.want.Time)
			case !got.Time.Equal(tc.want.Time):
				t.Errorf("the backstop is %s, want %s", got.Time, tc.want.Time)
			}
		})
	}
}

func TestTheLifetimeBackstopIsACopyOfTheLatch(t *testing.T) {
	latched := &metav1.Time{Time: latchInstant}
	status := statusHolding(instanceLatching(InstancePhaseCreated, latched))

	backstop := status.LifetimeBackstop()
	backstop.Time = latchInstant.Add(time.Hour)

	if !status.Instances[0].BackstopAt.Time.Equal(latchInstant) {
		t.Errorf("the latch moved to %s, want it left at %s", status.Instances[0].BackstopAt.Time, latchInstant)
	}
}
