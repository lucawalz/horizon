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
