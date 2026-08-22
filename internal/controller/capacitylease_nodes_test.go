package controller

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
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
		"instance "+name+" was created 6m ago and no node has registered; 0 of 1 nodes ready")

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

func pooledNode(name, providerID string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue},
		},
		Spec: corev1.NodeSpec{ProviderID: providerID},
	}
}

func (h *harness) assertEnqueued(node *corev1.Node, want ...string) {
	h.t.Helper()
	var got []string
	for _, request := range h.reconciler().leasesForNode(h.t.Context(), node) {
		if request.Namespace != "" {
			h.t.Errorf("request for %q carries namespace %q, but a capacity lease is cluster scoped", request.Name, request.Namespace)
		}
		got = append(got, request.Name)
	}
	if !slices.Equal(got, want) {
		h.t.Errorf("node %q enqueued %v, want %v", node.Name, got, want)
	}
}

func TestTheNodeWatchEnqueuesTheLeaseNamedByTheAdoptionLabel(t *testing.T) {
	h := newHarness(t)
	h.settle()

	node := pooledNode("adopted", "fake://unrelated")
	node.Labels[LeaseNameLabelKey] = h.name

	h.assertEnqueued(node, h.name)
}

func TestTheNodeWatchMatchesAnUnadoptedNodeByProviderID(t *testing.T) {
	h := newHarness(t)
	h.settle()

	entry := h.instanceStatus(h.instanceName(0))
	if entry.ProviderID == "" {
		t.Fatalf("instance %q carries no provider id to match on", entry.Name)
	}

	h.assertEnqueued(pooledNode("registered-under-another-name", entry.ProviderID), h.name)
}

func TestTheNodeWatchMatchesAnUnadoptedNodeByNameWhenNoProviderIDIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.prov.FailCreate = func(string) error { return errors.New("provider is unreachable") }
	h.settleIgnoringErrors(6)

	entry := h.instanceStatus(h.instanceName(0))
	if entry.ProviderID != "" {
		t.Fatalf("instance %q already carries a provider id", entry.Name)
	}

	h.assertEnqueued(pooledNode(entry.Name, ""), h.name)
}

func TestTheNodeWatchEnqueuesNothingForANodeNoLeaseClaims(t *testing.T) {
	h := newHarness(t)
	h.settle()

	h.assertEnqueued(pooledNode("home-0", "fake://claimed-by-nobody"))
}

func TestTheReadyInstantRefusesATransitionItCannotTrust(t *testing.T) {
	now := testInstant.Add(time.Hour)
	r := &CapacityLeaseReconciler{Clock: func() time.Time { return now }}

	tests := map[string]struct {
		accepted   time.Time
		unaccepted bool
		transition time.Time
		want       time.Time
	}{
		"a transition after acceptance is taken":        {accepted: testInstant, transition: testInstant.Add(time.Minute), want: testInstant.Add(time.Minute)},
		"a transition at acceptance is taken":           {accepted: testInstant, transition: testInstant, want: testInstant},
		"a transition before acceptance is not":         {accepted: testInstant, transition: testInstant.Add(-time.Minute), want: now},
		"a zero transition is not":                      {accepted: testInstant, transition: time.Time{}, want: now},
		"a zero transition against a zero lease is not": {accepted: time.Time{}, transition: time.Time{}, want: now},
		"an unaccepted lease trusts no transition":      {unaccepted: true, transition: testInstant.Add(time.Minute), want: now},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			lease := leaseExpiringIn(time.Hour)
			lease.Status.AcceptedAt = &metav1.Time{Time: tc.accepted}
			if tc.unaccepted {
				lease.Status.AcceptedAt = nil
			}
			if got := r.readyInstant(lease, tc.transition); !got.Equal(tc.want) {
				t.Errorf("ready instant is %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAnAcceptedLeaseReportsThatNoNodeIsReadyBeforeAnyInstanceExists(t *testing.T) {
	h := newHarness(t)

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("finalizer pass: %v", err)
	}
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("acceptance pass: %v", err)
	}

	if h.lease().Status.Instances != nil {
		t.Fatal("acceptance recorded an instance, so this no longer covers the empty lease")
	}
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionInstancesReady, reasonWaitingForNodes, "0 of 1 nodes ready")
}

func TestALeaseWhoseProviderNeverCreatesReportsThatItAwaitsAnInstance(t *testing.T) {
	h := newHarness(t)
	h.prov.FailCreate = func(string) error { return errors.New("provider is unreachable") }

	h.settleIgnoringErrors(6)
	h.clock.Advance(2 * time.Minute)
	h.settleIgnoringErrors(2)

	name := h.instanceName(0)
	if got := h.instanceStatus(name).Phase; got != v1alpha1.InstancePhaseIntended {
		t.Fatalf("instance phase is %q, want %q", got, v1alpha1.InstancePhaseIntended)
	}
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionInstancesReady, reasonAwaitingInstance,
		"instance "+name+" was requested 2m ago and no provider instance exists yet; 0 of 1 nodes ready")
}

func TestMatchNodePrefersAProviderIDMatchOverANameMatch(t *testing.T) {
	nodes := []corev1.Node{
		*pooledNode("burst-0", "fake://stale"),
		*pooledNode("burst-0-rebuilt", "fake://live"),
	}

	tests := map[string]struct {
		providerID string
		want       string
	}{
		"the provider id wins over an earlier name match": {providerID: "fake://live", want: "burst-0-rebuilt"},
		"an unmatched provider id falls back to the name": {providerID: "fake://gone", want: "burst-0"},
		"no provider id matches on the name alone":        {providerID: "", want: "burst-0"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			entry := &v1alpha1.InstanceStatus{Name: "burst-0", ProviderID: tc.providerID}
			matched := matchNode(nodes, entry)
			if matched == nil {
				t.Fatalf("no node matched instance %q", entry.Name)
			}
			if matched.Name != tc.want {
				t.Errorf("matched node %q, want %q", matched.Name, tc.want)
			}
		})
	}
}

func TestAnInstanceWithNoProviderIDDoesNotClaimANodeWithNoProviderID(t *testing.T) {
	entry := &v1alpha1.InstanceStatus{Name: "burst-0"}
	node := pooledNode("home-0", "")

	if instanceMatchesNode(entry, node) {
		t.Errorf("instance %q claims unrelated node %q on two empty provider ids", entry.Name, node.Name)
	}
	if matched := matchNode([]corev1.Node{*node}, entry); matched != nil {
		t.Errorf("instance %q adopted unrelated node %q", entry.Name, matched.Name)
	}
}

func TestTheNodeWatchDoesNotClaimAPooledNodeForAnInstanceWithNoProviderID(t *testing.T) {
	h := newHarness(t)
	h.prov.FailCreate = func(string) error { return errors.New("provider is unreachable") }
	h.settleIgnoringErrors(6)

	entry := h.instanceStatus(h.instanceName(0))
	if entry.ProviderID != "" {
		t.Fatalf("instance %q already carries a provider id", entry.Name)
	}

	h.assertEnqueued(pooledNode("home-0", ""))
}

func TestTheWaitingStagesAgreeWithTheReasonsTheyReport(t *testing.T) {
	for stage, waiting := range waitingStages {
		if waiting.reason != string(stage) {
			t.Errorf("stage %q reports reason %q, want them to be the same name", stage, waiting.reason)
		}
	}
	for _, stage := range []v1alpha1.InstanceStage{
		v1alpha1.InstanceStageAwaitingInstance,
		v1alpha1.InstanceStageAwaitingRegistration,
		v1alpha1.InstanceStageAwaitingReady,
	} {
		if _, waiting := waitingStages[stage]; !waiting {
			t.Errorf("stage %q reports no waiting reason", stage)
		}
	}
	if _, waiting := waitingStages[v1alpha1.InstanceStageReady]; waiting {
		t.Error("a ready instance is reported as a waiting stage")
	}
}

func TestElapsedSinceStaysReadableForEveryTimestampItIsGiven(t *testing.T) {
	tests := map[string]struct {
		instant *metav1.Time
		want    string
	}{
		"a past timestamp reads as elapsed":      {instant: &metav1.Time{Time: testInstant.Add(-6 * time.Minute)}, want: "6m"},
		"an absent timestamp says so":            {instant: nil, want: "an unknown time"},
		"a timestamp ahead of the clock reads 0": {instant: &metav1.Time{Time: testInstant.Add(time.Hour)}, want: "0s"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := elapsedSince(tc.instant, testInstant); got != tc.want {
				t.Errorf("elapsed reads %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTheLeaseWatchWakesOnTheLabelItsMapFunctionReads(t *testing.T) {
	unadopted := pooledNode("burst-0", "fake://1")
	adopted := pooledNode("burst-0", "fake://1")
	adopted.Labels[LeaseNameLabelKey] = "burst"
	annotated := pooledNode("burst-0", "fake://1")
	annotated.Annotations = map[string]string{provider.WatchdogDeadlineAnnotationKey: "1785931200"}

	signals := nodeSignals(LeaseNameLabelKey)
	if !signals.Update(event.UpdateEvent{ObjectOld: unadopted, ObjectNew: adopted}) {
		t.Error("adoption does not wake the watch, so the map function never sees the label it reads")
	}
	if signals.Update(event.UpdateEvent{ObjectOld: unadopted, ObjectNew: annotated}) {
		t.Error("a watchdog renewal wakes the lease reconciler, which lists nodes on every wake")
	}
}
