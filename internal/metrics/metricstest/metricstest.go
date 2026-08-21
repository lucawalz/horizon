// Package metricstest reads the horizon metric surface back out of the registry every test shares.
package metricstest

import (
	"slices"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	CatalogueAge       = "horizon_instance_type_catalogue_age_seconds"
	CatalogueRefresh   = "horizon_instance_type_catalogue_refresh_total"
	InstanceTypeCores  = "horizon_instance_type_cpu_cores"
	InstanceTypeMemory = "horizon_instance_type_memory_bytes"
	InstanceTypePrice  = "horizon_instance_type_price_estimate"
	ProviderRequests   = "horizon_provider_request_duration_seconds"
)

type sample struct {
	counter float64
	count   uint64
	sum     float64
}

// the registry outlives every test, so an absolute assertion breaks under repeated runs of the same suite
type Snapshot map[string]sample

func Take(t *testing.T) Snapshot {
	t.Helper()
	taken := Snapshot{}
	for _, family := range families(t) {
		for _, series := range family.GetMetric() {
			taken[key(family.GetName(), series)] = sample{
				counter: series.GetCounter().GetValue(),
				count:   series.GetHistogram().GetSampleCount(),
				sum:     series.GetHistogram().GetSampleSum(),
			}
		}
	}
	return taken
}

func (s Snapshot) Counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	series := Series(t, name, labels)
	if series == nil {
		return 0
	}
	return series.GetCounter().GetValue() - s[key(name, series)].counter
}

func (s Snapshot) Observations(t *testing.T, name string, labels map[string]string) (uint64, float64) {
	t.Helper()
	series := Series(t, name, labels)
	if series == nil {
		return 0, 0
	}
	was := s[key(name, series)]
	return series.GetHistogram().GetSampleCount() - was.count, series.GetHistogram().GetSampleSum() - was.sum
}

func Gauge(t *testing.T, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	series := Series(t, name, labels)
	if series == nil {
		return 0, false
	}
	return series.GetGauge().GetValue(), true
}

func Series(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	matching := AllSeries(t, name, labels)
	if len(matching) == 0 {
		return nil
	}
	return matching[0]
}

func AllSeries(t *testing.T, name string, labels map[string]string) []*dto.Metric {
	t.Helper()
	var matching []*dto.Metric
	for _, family := range families(t) {
		if family.GetName() != name {
			continue
		}
		for _, series := range family.GetMetric() {
			if carries(series, labels) {
				matching = append(matching, series)
			}
		}
	}
	return matching
}

func AssertNoSeriesCarries(t *testing.T, forbidden string) {
	t.Helper()
	for _, family := range families(t) {
		for _, series := range family.GetMetric() {
			for _, label := range series.GetLabel() {
				if label.GetValue() == forbidden {
					t.Errorf("%s carries %s=%q", family.GetName(), label.GetName(), forbidden)
				}
			}
		}
	}
}

func families(t *testing.T) []*dto.MetricFamily {
	t.Helper()
	gathered, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather the metric registry: %v", err)
	}
	return gathered
}

func carries(series *dto.Metric, labels map[string]string) bool {
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

func key(name string, series *dto.Metric) string {
	pairs := make([]string, 0, len(series.GetLabel()))
	for _, label := range series.GetLabel() {
		pairs = append(pairs, label.GetName()+"="+label.GetValue())
	}
	slices.Sort(pairs)
	return name + "{" + strings.Join(pairs, ",") + "}"
}
