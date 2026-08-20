package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

func TestLeaseStateCollectorEmitsCountsAndNoLeaseIdentity(t *testing.T) {
	if err := SetLeaseStateSource(func() LeaseState {
		return LeaseState{
			Phases: map[v1alpha1.LeasePhase]int{
				v1alpha1.LeasePhaseActive:   2,
				v1alpha1.LeasePhaseExpiring: 1,
			},
			Conditions: map[LeaseCondition]int{
				{Type: v1alpha1.ConditionAccepted, Status: metav1.ConditionTrue}:        2,
				{Type: v1alpha1.ConditionInstancesReady, Status: metav1.ConditionFalse}: 1,
			},
		}
	}); err != nil {
		t.Fatalf("set the lease state source: %v", err)
	}

	expected := `
# HELP horizon_leases Number of capacity leases in each phase.
# TYPE horizon_leases gauge
horizon_leases{phase="Active"} 2
horizon_leases{phase="Expiring"} 1
# HELP horizon_lease_status_condition Number of capacity leases reporting each status condition.
# TYPE horizon_lease_status_condition gauge
horizon_lease_status_condition{condition="Accepted",status="True"} 2
horizon_lease_status_condition{condition="InstancesReady",status="False"} 1
`
	if err := testutil.CollectAndCompare(leaseState, strings.NewReader(expected)); err != nil {
		t.Errorf("the lease state collector emitted the wrong series: %v", err)
	}
}

func TestLeaseStateCollectorReflectsTheLatestSource(t *testing.T) {
	if err := SetLeaseStateSource(func() LeaseState {
		return LeaseState{Phases: map[v1alpha1.LeasePhase]int{v1alpha1.LeasePhasePending: 7}}
	}); err != nil {
		t.Fatalf("set the lease state source: %v", err)
	}

	expected := `
# HELP horizon_leases Number of capacity leases in each phase.
# TYPE horizon_leases gauge
horizon_leases{phase="Pending"} 7
`
	if err := testutil.CollectAndCompare(leaseState, strings.NewReader(expected), "horizon_leases"); err != nil {
		t.Errorf("the collector did not read the replaced source: %v", err)
	}
}

func TestSetLeaseStateSourceRejectsAMissingSource(t *testing.T) {
	if err := SetLeaseStateSource(nil); err == nil {
		t.Error("a missing lease state source was accepted, want an error")
	}
}

func TestAPanickingLeaseStateSourceCostsOnlyItsOwnFamilies(t *testing.T) {
	restored := leaseState.source.Load()
	t.Cleanup(func() { leaseState.source.Store(restored) })

	if err := SetLeaseStateSource(func() LeaseState { panic("the lease informer has not started") }); err != nil {
		t.Fatalf("set the lease state source: %v", err)
	}
	RecordOrphanInstanceDeleted("panicking", "hel1")

	response := httptest.NewRecorder()
	promhttp.HandlerFor(ctrlmetrics.Registry, promhttp.HandlerOpts{}).
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("the scrape returned %d, want %d", response.Code, http.StatusOK)
	}

	body := response.Body.String()
	for _, family := range []string{"horizon_leases", "horizon_lease_status_condition"} {
		if strings.Contains(body, family) {
			t.Errorf("a failing lease state source still emitted %s", family)
		}
	}
	if !strings.Contains(body, "horizon_orphan_instances_deleted_total") {
		t.Error("a failing lease state source suppressed an unrelated metric")
	}
}
