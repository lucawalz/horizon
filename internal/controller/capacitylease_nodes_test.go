package controller

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

func stagedInstance(name string, phase v1alpha1.InstancePhase, created time.Time) *v1alpha1.InstanceStatus {
	return &v1alpha1.InstanceStatus{
		Name:      name,
		Phase:     phase,
		CreatedAt: &metav1.Time{Time: created},
	}
}

func readyNode(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func TestTheInstanceStageFollowsThePhaseAndTheNode(t *testing.T) {
	tests := map[string]struct {
		phase v1alpha1.InstancePhase
		node  *corev1.Node
		want  v1alpha1.InstanceStage
	}{
		"an intended instance awaits the provider": {
			phase: v1alpha1.InstancePhaseIntended,
			want:  v1alpha1.InstanceStageAwaitingInstance,
		},
		"a created instance with no node awaits registration": {
			phase: v1alpha1.InstancePhaseCreated,
			want:  v1alpha1.InstanceStageAwaitingRegistration,
		},
		"a registered node that is not ready awaits readiness": {
			phase: v1alpha1.InstancePhaseCreated,
			node:  readyNode("burst-0", false),
			want:  v1alpha1.InstanceStageAwaitingReady,
		},
		"a ready node is ready": {
			phase: v1alpha1.InstancePhaseJoined,
			node:  readyNode("burst-0", true),
			want:  v1alpha1.InstanceStageReady,
		},
		"a released instance names no waiting stage": {
			phase: v1alpha1.InstancePhaseReleased,
			want:  "",
		},
		"a draining instance names no waiting stage": {
			phase: v1alpha1.InstancePhaseDraining,
			want:  "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			entry := stagedInstance("burst-0", tc.phase, testInstant)
			if got := instanceStage(entry, tc.node); got != tc.want {
				t.Errorf("stage is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTheBlockingStageChoosesTheConditionReasonAndNamesTheInstance(t *testing.T) {
	created := testInstant.Add(-6 * time.Minute)

	tests := map[string]struct {
		stages  []v1alpha1.InstanceStage
		reason  string
		mention string
	}{
		"no provider instance yet": {
			stages:  []v1alpha1.InstanceStage{v1alpha1.InstanceStageAwaitingInstance},
			reason:  reasonAwaitingInstance,
			mention: "instance burst-0 was requested 6m ago and no provider instance exists yet",
		},
		"a server with no node": {
			stages:  []v1alpha1.InstanceStage{v1alpha1.InstanceStageAwaitingRegistration},
			reason:  reasonAwaitingRegistration,
			mention: "instance burst-0 was created 6m ago and no node has registered",
		},
		"a node that is not ready": {
			stages:  []v1alpha1.InstanceStage{v1alpha1.InstanceStageAwaitingReady},
			reason:  reasonAwaitingReady,
			mention: "instance burst-0 was created 6m ago and its node is not ready",
		},
		"the least advanced stage wins": {
			stages: []v1alpha1.InstanceStage{
				v1alpha1.InstanceStageReady,
				v1alpha1.InstanceStageAwaitingReady,
				v1alpha1.InstanceStageAwaitingRegistration,
			},
			reason:  reasonAwaitingRegistration,
			mention: "instance burst-2 was created 6m ago and no node has registered",
		},
		"a released entry is not a waiting stage": {
			stages:  []v1alpha1.InstanceStage{""},
			reason:  reasonWaitingForNodes,
			mention: "0 of 1 nodes ready",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			lease := leaseExpiringIn(time.Hour)
			for i, stage := range tc.stages {
				entry := stagedInstance(instanceName(lease, i), v1alpha1.InstancePhaseCreated, created)
				entry.Stage = stage
				lease.Status.Instances = append(lease.Status.Instances, *entry)
			}

			reason, message := waitingCondition(lease, 0, len(tc.stages), testInstant)
			if reason != tc.reason {
				t.Errorf("reason is %q, want %q", reason, tc.reason)
			}
			if !strings.Contains(message, tc.mention) {
				t.Errorf("message is %q, want it to mention %q", message, tc.mention)
			}
		})
	}
}

func TestALeaseWithNoInstanceEntryStillReportsWaitingForNodes(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)

	reason, message := waitingCondition(lease, 0, 1, testInstant)
	if reason != reasonWaitingForNodes {
		t.Errorf("reason is %q, want %q", reason, reasonWaitingForNodes)
	}
	if message != "0 of 1 nodes ready" {
		t.Errorf("message is %q, want the bare node count", message)
	}
}

func TestAWaitingLeaseReportsWhichStageIsBlockingIt(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.clock.Advance(6 * time.Minute)
	h.settle()
	h.assertConditionDetail(v1alpha1.ConditionInstancesReady, reasonAwaitingRegistration,
		"0 of 1 nodes ready; instance "+name+" was created 6m ago and no node has registered")

	h.joinNode(name, false)
	h.settle()
	h.assertConditionDetail(v1alpha1.ConditionInstancesReady, reasonAwaitingReady,
		"instance "+name+" was created 6m ago and its node is not ready")
}

func TestReadyAtIsTheNodeReadyTransitionNotTheReconcileClock(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, false)
	transition := h.clock.Now().Add(20 * time.Second)
	h.clock.Advance(5 * time.Minute)
	h.setNodeReadyAt(name, true, transition)
	h.settle()

	readyAt := h.lease().Status.ReadyAt
	if readyAt == nil {
		t.Fatal("readyAt was not set when the node became ready")
	}
	if !readyAt.Time.Equal(transition) {
		t.Errorf("readyAt is %s, want the node ready transition %s", readyAt.Time, transition)
	}
	h.assertObservations(leaseReadySecondsMetric,
		map[string]string{"instance_type": testSize, "selection": pinnedSelection}, 1, 20)
}

func TestAZeroReadyTransitionFallsBackToTheReconcileClock(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, false)
	h.clock.Advance(5 * time.Minute)
	h.setNodeReadyAt(name, true, time.Time{})
	h.settle()

	readyAt := h.lease().Status.ReadyAt
	if readyAt == nil {
		t.Fatal("readyAt was not set when the node became ready")
	}
	if !readyAt.Time.Equal(h.clock.Now()) {
		t.Errorf("readyAt is %s, want the reconcile clock %s", readyAt.Time, h.clock.Now())
	}
}

func TestAReadyTransitionBeforeAcceptanceFallsBackToTheReconcileClock(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, false)
	accepted := h.lease().Status.AcceptedAt.Time
	h.clock.Advance(5 * time.Minute)
	h.setNodeReadyAt(name, true, accepted.Add(-time.Hour))
	h.settle()

	readyAt := h.lease().Status.ReadyAt
	if readyAt == nil {
		t.Fatal("readyAt was not set when the node became ready")
	}
	if !readyAt.Time.Equal(h.clock.Now()) {
		t.Errorf("readyAt is %s, want the reconcile clock %s", readyAt.Time, h.clock.Now())
	}
	if readyAt.Time.Before(accepted) {
		t.Errorf("readyAt %s precedes acceptance %s, which records a negative duration", readyAt.Time, accepted)
	}
	h.assertObservations(leaseReadySecondsMetric,
		map[string]string{"instance_type": testSize, "selection": pinnedSelection}, 1, 300)
}

func TestReadyAtIsTheLatestTransitionAcrossReplicas(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) { lease.Spec.Replicas = 2 })
	h.settle()

	first, second := h.instanceName(0), h.instanceName(1)
	h.joinNode(first, false)
	h.joinNode(second, false)

	earliest := h.clock.Now().Add(30 * time.Second)
	latest := h.clock.Now().Add(90 * time.Second)
	h.clock.Advance(10 * time.Minute)
	h.setNodeReadyAt(first, true, earliest)
	h.setNodeReadyAt(second, true, latest)
	h.settle()

	readyAt := h.lease().Status.ReadyAt
	if readyAt == nil {
		t.Fatal("readyAt was not set when both nodes became ready")
	}
	if !readyAt.Time.Equal(latest) {
		t.Errorf("readyAt is %s, want the slowest node's transition %s", readyAt.Time, latest)
	}
}
