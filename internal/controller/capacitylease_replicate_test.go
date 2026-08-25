package controller

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
)

const testBurstReplicas = 3

func replicated(t *testing.T, mutators ...func(*v1alpha1.CapacityLease)) *harness {
	t.Helper()
	h := newHarness(t, append([]func(*v1alpha1.CapacityLease){replicating(testBurstReplicas, testWorkloadNS)}, mutators...)...)
	h.settle()
	h.joinNode(h.instanceName(0), true)
	return h
}

func TestReplicateModeRunsACopyAndLeavesTheOriginalAlone(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadReplicable, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionWorkloadReplicable, reasonReplicated, "1 workloads replicated onto burst capacity")

	copies := h.burstCopiesIn(testWorkloadNS)
	if len(copies) != 1 {
		t.Fatalf("the namespace holds %d burst copies, want one", len(copies))
	}
	if got := *copies[0].Spec.Replicas; got != testBurstReplicas {
		t.Errorf("the copy runs %d pods, want the %d the lease asked for", got, testBurstReplicas)
	}
	if got := h.lease().Status.MigratedWorkloads; len(got) != 1 || got[0] != testWorkloadNS+"/deployment/"+copies[0].Name {
		t.Errorf("the lease records %v, want the copy teardown has to delete", got)
	}
	if _, moved := h.deploymentAnnotations()[k8s.PrePlacementAnnotationKey]; moved {
		t.Error("replicate mode wrote a saved placement onto the original")
	}
}

func TestTeardownFollowsTheModeTheLeasePlacedIn(t *testing.T) {
	tests := []struct {
		name          string
		placed        v1alpha1.WorkloadMode
		asked         v1alpha1.WorkloadMode
		wantReplicate bool
		wantCondition string
	}{
		{"a copy the spec now calls a move", v1alpha1.WorkloadModeReplicate, v1alpha1.WorkloadModeMove, true, v1alpha1.ConditionWorkloadReplicable},
		{"a move the spec now calls a copy", v1alpha1.WorkloadModeMove, v1alpha1.WorkloadModeReplicate, false, v1alpha1.ConditionWorkloadMigrated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lease := &v1alpha1.CapacityLease{
				Spec:   v1alpha1.CapacityLeaseSpec{Workload: &v1alpha1.WorkloadRef{Mode: tc.asked}},
				Status: v1alpha1.CapacityLeaseStatus{PlacedWorkloadMode: tc.placed},
			}

			if got := replicateMode(lease); got != tc.wantReplicate {
				t.Errorf("teardown reads the lease as replicating=%t, want %t: what the lease did is what it has to undo", got, tc.wantReplicate)
			}
			if got := workloadPlacedCondition(lease); got != tc.wantCondition {
				t.Errorf("the lease reports placement on %s, want %s", got, tc.wantCondition)
			}
		})
	}
}

func TestReplicateModeRecordsTheModeItPlacedIn(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.settle()

	if got := h.lease().Status.PlacedWorkloadMode; got != v1alpha1.WorkloadModeReplicate {
		t.Errorf("the lease recorded mode %q, want %q so teardown deletes the copy it made", got, v1alpha1.WorkloadModeReplicate)
	}
}

func TestTeardownDeletesTheCopyThoughTheSpecNoLongerNamesAWorkload(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.settle()
	h.dropWorkloadTarget()

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("teardown pass: %v", err)
	}

	if got := len(h.burstCopiesIn(testWorkloadNS)); got != 0 {
		t.Errorf("teardown left %d burst copies running because the spec stopped naming a workload", got)
	}
	if got := h.lease().Status.MigratedWorkloads; len(got) != 0 {
		t.Errorf("the lease still owes %v after its copies were deleted", got)
	}
}

func TestReplicateModeReportsNoMigratabilityVerdict(t *testing.T) {
	h := replicated(t)
	h.seedWorkload(func(d *appsv1.Deployment) { d.Spec.Paused = true })
	h.settle()

	if cond := meta.FindStatusCondition(h.lease().Status.Conditions, v1alpha1.ConditionWorkloadMigratable); cond != nil {
		t.Errorf("replicate mode reported %s=%s, a verdict about a rollout it never performs", cond.Type, cond.Status)
	}
	if cond := meta.FindStatusCondition(h.lease().Status.Conditions, v1alpha1.ConditionWorkloadMigrated); cond != nil {
		t.Errorf("replicate mode reported %s=%s, though it moved nothing", cond.Type, cond.Status)
	}
}

func TestReplicateModeFailsWhenTheSelectorNamesNothing(t *testing.T) {
	h := replicated(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "batch"}}
	})
	h.seedWorkload()
	h.settleIgnoringErrors(3)

	h.assertCondition(v1alpha1.ConditionWorkloadReplicable, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionWorkloadReplicable, reasonNoMatchingWorkloads, testWorkloadNS)
	if got := len(h.burstCopiesIn(testWorkloadNS)); got != 0 {
		t.Errorf("a selector that names nothing still produced %d copies", got)
	}
}

func TestReplicateModeSkipsAnAutoscaledWorkloadAndNamesMoveMode(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.seedAutoscalerFor("api")
	h.settleIgnoringErrors(3)

	h.assertCondition(v1alpha1.ConditionWorkloadReplicable, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionWorkloadReplicable, reasonEveryWorkloadSkipped, "move mode")
	if got := len(h.burstCopiesIn(testWorkloadNS)); got != 0 {
		t.Errorf("an autoscaled workload was copied %d times, and the copy is what scales the original down", got)
	}
	warnings := h.lease().Status.MigrationWarnings
	if len(warnings) != 1 || warnings[0].Workload != testWorkloadNS+"/deployment/api" ||
		!strings.Contains(strings.Join(warnings[0].Reasons, ","), k8s.ReasonAutoscalerTargeted) {
		t.Errorf("the lease warns %+v, want the autoscaled workload named with its reason", warnings)
	}
}

func TestReplicateModeDeletesTheCopyAtTeardown(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.settle()
	if got := len(h.burstCopiesIn(testWorkloadNS)); got != 1 {
		t.Fatalf("the namespace holds %d burst copies before teardown, want one", got)
	}

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("teardown pass: %v", err)
	}

	if got := len(h.burstCopiesIn(testWorkloadNS)); got != 0 {
		t.Errorf("teardown left %d burst copies running on capacity the lease is about to return", got)
	}
	if got := h.lease().Status.MigratedWorkloads; len(got) != 0 {
		t.Errorf("the lease still owes %v after its copies were deleted", got)
	}
	h.assertConditionDetail(v1alpha1.ConditionWorkloadReplicable, reasonCopiesDeleted, "burst copies deleted")
	if h.deploymentIn(testWorkloadNS, "api") == nil {
		t.Error("teardown deleted the original alongside the copy")
	}
}

func TestReplicateModeWithholdsReleaseUntilTheCopysPodsAreGone(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.settle()
	node := h.instanceName(0)

	h.deleteLease()
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("teardown pass: %v", err)
	}
	h.seedPod("api-burst-0", testWorkloadNS, node)

	for range 5 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("reconcile while a copy pod sits on the burst node: %v", err)
		}
	}

	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances, want the release withheld until the copy's pods are gone", got)
	}
	if _, ok := h.node(node); !ok {
		t.Error("the burst node was drained while a copy pod still ran on it")
	}
}

func TestReplicateModeWarnsAboutADisruptionBudgetWithoutSkipping(t *testing.T) {
	h := replicated(t)
	h.seedWorkload()
	h.seedDisruptionBudgetFor("api")
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadReplicable, metav1.ConditionTrue)
	if got := len(h.burstCopiesIn(testWorkloadNS)); got != 1 {
		t.Errorf("the namespace holds %d burst copies, want the budget to warn rather than stop the copy", got)
	}
	warnings := h.lease().Status.MigrationWarnings
	if len(warnings) != 1 || !strings.Contains(strings.Join(warnings[0].Reasons, ","), k8s.ReasonBudgetSpansCopy) {
		t.Errorf("the lease warns %+v, want the disruption budget named", warnings)
	}
}
