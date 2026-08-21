package controller

import (
	"maps"
	"testing"

	dto "github.com/prometheus/client_model/go"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	leaseReadySecondsMetric   = "horizon_lease_ready_seconds"
	leaseReleaseSecondsMetric = "horizon_lease_release_seconds"
	leaseTerminalMetric       = "horizon_lease_terminal_total"
	instanceReleasedMetric    = "horizon_instance_released_total"
	instanceSecondsMetric     = "horizon_instance_seconds_total"
	instanceBilledHoursMetric = "horizon_instance_billed_hours_total"
	instanceUndatedMetric     = "horizon_instance_lifetime_unknown_total"
)

func gatheredSeries(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()

	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather the metric registry: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, series := range family.GetMetric() {
			if seriesCarries(series, labels) {
				return series
			}
		}
	}
	return nil
}

func seriesCarries(series *dto.Metric, labels map[string]string) bool {
	matched := 0
	for _, label := range series.GetLabel() {
		want, checked := labels[label.GetName()]
		if !checked {
			continue
		}
		if label.GetValue() != want {
			return false
		}
		matched++
	}
	return matched == len(labels)
}

func (h *harness) ownLabels(extra map[string]string) map[string]string {
	labels := map[string]string{"provider": h.name, "region": testRegion}
	maps.Copy(labels, extra)
	return labels
}

func counterFor(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	series := gatheredSeries(t, name, labels)
	if series == nil {
		return 0
	}
	return series.GetCounter().GetValue()
}

func (h *harness) counter(name string, extra map[string]string) float64 {
	h.t.Helper()
	return counterFor(h.t, name, h.ownLabels(extra))
}

func (h *harness) assertCounter(name string, extra map[string]string, want float64) {
	h.t.Helper()
	if got := h.counter(name, extra); got != want {
		h.t.Errorf("%s%v is %v, want %v", name, extra, got, want)
	}
}

func (h *harness) observations(name string, extra map[string]string) (uint64, float64) {
	h.t.Helper()
	series := gatheredSeries(h.t, name, h.ownLabels(extra))
	if series == nil {
		return 0, 0
	}
	return series.GetHistogram().GetSampleCount(), series.GetHistogram().GetSampleSum()
}

func (h *harness) assertObservations(name string, extra map[string]string, wantCount uint64, wantSum float64) {
	h.t.Helper()
	count, sum := h.observations(name, extra)
	if count != wantCount {
		h.t.Errorf("%s%v holds %d observations, want %d", name, extra, count, wantCount)
	}
	if sum != wantSum {
		h.t.Errorf("%s%v sums to %v, want %v", name, extra, sum, wantSum)
	}
}
