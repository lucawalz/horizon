package controller

import (
	"maps"
	"slices"
	"strings"
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

func gatherFamilies(t *testing.T) []*dto.MetricFamily {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather the metric registry: %v", err)
	}
	return families
}

func gatheredSeries(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	for _, family := range gatherFamilies(t) {
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

func assertNoSeriesCarries(t *testing.T, value string) {
	t.Helper()
	for _, family := range gatherFamilies(t) {
		for _, series := range family.GetMetric() {
			for _, label := range series.GetLabel() {
				if label.GetValue() == value {
					t.Errorf("%s carries %s=%q", family.GetName(), label.GetName(), value)
				}
			}
		}
	}
}

type seriesValue struct {
	counter float64
	count   uint64
	sum     float64
}

// the registry outlives every test, so an absolute assertion breaks under repeated runs of the same suite
type seriesSnapshot map[string]seriesValue

func snapshotSeries(t *testing.T) seriesSnapshot {
	t.Helper()
	taken := seriesSnapshot{}
	for _, family := range gatherFamilies(t) {
		for _, series := range family.GetMetric() {
			taken[seriesKey(family.GetName(), series)] = seriesValue{
				counter: series.GetCounter().GetValue(),
				count:   series.GetHistogram().GetSampleCount(),
				sum:     series.GetHistogram().GetSampleSum(),
			}
		}
	}
	return taken
}

func seriesKey(name string, series *dto.Metric) string {
	pairs := make([]string, 0, len(series.GetLabel()))
	for _, label := range series.GetLabel() {
		pairs = append(pairs, label.GetName()+"="+label.GetValue())
	}
	slices.Sort(pairs)
	return name + "{" + strings.Join(pairs, ",") + "}"
}

func (s seriesSnapshot) counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	series := gatheredSeries(t, name, labels)
	if series == nil {
		return 0
	}
	return series.GetCounter().GetValue() - s[seriesKey(name, series)].counter
}

func (s seriesSnapshot) observations(t *testing.T, name string, labels map[string]string) (uint64, float64) {
	t.Helper()
	series := gatheredSeries(t, name, labels)
	if series == nil {
		return 0, 0
	}
	was := s[seriesKey(name, series)]
	return series.GetHistogram().GetSampleCount() - was.count, series.GetHistogram().GetSampleSum() - was.sum
}

func (h *harness) ownLabels(extra map[string]string) map[string]string {
	labels := map[string]string{"provider": h.name, "region": testRegion}
	maps.Copy(labels, extra)
	return labels
}

func (h *harness) assertCounter(name string, extra map[string]string, want float64) {
	h.t.Helper()
	h.assertCounterAt(name, h.ownLabels(extra), want)
}

func (h *harness) assertCounterAt(name string, labels map[string]string, want float64) {
	h.t.Helper()
	if got := h.baseline.counter(h.t, name, labels); got != want {
		h.t.Errorf("%s%v is %v, want %v", name, labels, got, want)
	}
}

func (h *harness) observations(name string, extra map[string]string) (uint64, float64) {
	h.t.Helper()
	return h.baseline.observations(h.t, name, h.ownLabels(extra))
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
