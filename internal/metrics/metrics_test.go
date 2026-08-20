package metrics

import (
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type familyShape struct {
	kind   string
	labels []string
}

var wantFamilies = map[string]familyShape{
	"horizon_lease_ready_seconds":                   {"HISTOGRAM", []string{"instance_type", "provider", "region", "selection"}},
	"horizon_lease_release_seconds":                 {"HISTOGRAM", []string{"instance_type", "path", "provider", "region"}},
	"horizon_lease_terminal_total":                  {"COUNTER", []string{"outcome", "provider", "region"}},
	"horizon_instance_released_total":               {"COUNTER", []string{"instance_type", "path", "provider", "region"}},
	"horizon_instance_seconds_total":                {"COUNTER", []string{"instance_type", "provider", "region"}},
	"horizon_instance_billed_hours_total":           {"COUNTER", []string{"instance_type", "provider", "region"}},
	"horizon_instance_type_price_estimate":          {"GAUGE", []string{"instance_type", "provider", "region"}},
	"horizon_instance_type_cpu_cores":               {"GAUGE", []string{"instance_type", "provider"}},
	"horizon_instance_type_memory_bytes":            {"GAUGE", []string{"instance_type", "provider"}},
	"horizon_instance_type_selected_total":          {"COUNTER", []string{"instance_type", "provider", "region", "strategy"}},
	"horizon_instance_type_selection_failed_total":  {"COUNTER", []string{"provider", "reason", "region", "strategy"}},
	"horizon_instance_type_catalogue_age_seconds":   {"GAUGE", []string{"provider"}},
	"horizon_instance_type_catalogue_refresh_total": {"COUNTER", []string{"provider", "result"}},
	"horizon_watchdog_renewals_total":               {"COUNTER", []string{"provider", "result"}},
	"horizon_orphan_instances_deleted_total":        {"COUNTER", []string{"provider", "region"}},
	"horizon_leases":                                {"GAUGE", []string{"phase"}},
	"horizon_lease_status_condition":                {"GAUGE", []string{"condition", "status"}},
	"horizon_provider_request_duration_seconds":     {"HISTOGRAM", []string{"operation", "provider", "result"}},
}

func populate(t *testing.T) map[string]familyShape {
	t.Helper()

	const config, region, instanceType = "surface", "hel1", "cx23"

	ObserveLeaseReady(config, region, instanceType, SelectionPinned, 71*time.Second)
	ObserveLeaseRelease(config, region, instanceType, PathController, 12*time.Second)
	RecordLeaseTerminal(config, region, OutcomeReleased)
	RecordInstanceReleased(config, region, instanceType, PathController, 10*time.Minute)
	SetInstanceTypePrice(config, region, instanceType, 0.0074)
	SetInstanceTypeCapacity(config, instanceType, 2, 4<<30)
	RecordInstanceTypeSelected(config, region, instanceType, SelectionLowestPrice)
	RecordSelectionFailed(config, region, SelectionLowestPrice, ReasonNoMatch)
	SetCatalogueAge(config, time.Minute)
	RecordCatalogueRefresh(config, ResultSuccess)
	RecordWatchdogRenewal(config, ResultSuccess)
	RecordOrphanInstanceDeleted(config, region)
	ObserveProviderRequest(config, OperationCreate, ResultSuccess, 250*time.Millisecond)

	if err := SetLeaseStateSource(func() LeaseState {
		return LeaseState{
			Phases:     map[v1alpha1.LeasePhase]int{v1alpha1.LeasePhaseActive: 1},
			Conditions: map[LeaseCondition]int{{Type: v1alpha1.ConditionAccepted, Status: metav1.ConditionTrue}: 1},
		}
	}); err != nil {
		t.Fatalf("set the lease state source: %v", err)
	}

	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather the registry: %v", err)
	}

	got := map[string]familyShape{}
	for _, family := range families {
		name := family.GetName()
		if len(name) < len("horizon_") || name[:len("horizon_")] != "horizon_" {
			continue
		}
		shape := familyShape{kind: family.GetType().String()}
		for _, label := range family.GetMetric()[0].GetLabel() {
			shape.labels = append(shape.labels, label.GetName())
		}
		slices.Sort(shape.labels)
		got[name] = shape
	}
	return got
}

func TestRegisteredSurfaceMatchesTheDesignedMetricSet(t *testing.T) {
	got := populate(t)

	for name, want := range wantFamilies {
		have, ok := got[name]
		if !ok {
			t.Errorf("metric %q is not registered", name)
			continue
		}
		if have.kind != want.kind {
			t.Errorf("metric %q is a %s, want a %s", name, have.kind, want.kind)
		}
		if !slices.Equal(have.labels, want.labels) {
			t.Errorf("metric %q carries labels %v, want %v", name, have.labels, want.labels)
		}
	}
	for name := range got {
		if _, ok := wantFamilies[name]; !ok {
			t.Errorf("metric %q is registered but undocumented", name)
		}
	}
}

func TestEveryRegisteredCollectorIsDocumented(t *testing.T) {
	descs := make(chan *prometheus.Desc, len(wantFamilies)*2)
	for _, collector := range registered {
		collector.Describe(descs)
	}
	close(descs)

	described := 0
	for range descs {
		described++
	}
	if described != len(wantFamilies) {
		t.Errorf("the registered collectors describe %d metrics, want %d", described, len(wantFamilies))
	}
}

func TestNoMetricCarriesAnUnboundedLabel(t *testing.T) {
	bounded := []string{
		"condition", "instance_type", "operation", "outcome", "path", "phase",
		"provider", "reason", "region", "result", "selection", "status", "strategy",
	}

	for name, shape := range populate(t) {
		for _, label := range shape.labels {
			if !slices.Contains(bounded, label) {
				t.Errorf("metric %q carries label %q, which has no bounded domain", name, label)
			}
		}
	}
}

func TestHistogramBucketsArePinned(t *testing.T) {
	populate(t)

	cases := map[string][]float64{
		"horizon_lease_ready_seconds":               {15, 30, 45, 60, 75, 90, 120, 180, 300, 600},
		"horizon_lease_release_seconds":             {1, 2, 5, 10, 15, 20, 30, 45, 60, 120, 300, 600},
		"horizon_provider_request_duration_seconds": {0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
	}

	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather the registry: %v", err)
	}
	for _, family := range families {
		want, checked := cases[family.GetName()]
		if !checked {
			continue
		}
		var got []float64
		for _, bucket := range family.GetMetric()[0].GetHistogram().GetBucket() {
			got = append(got, bucket.GetUpperBound())
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s has buckets %v, want %v", family.GetName(), got, want)
		}
		delete(cases, family.GetName())
	}
	for name := range cases {
		t.Errorf("%s was not gathered", name)
	}
}
