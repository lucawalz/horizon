package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func seriesFor(t *testing.T, collector prometheus.Collector, providerConfig string) []*dto.Metric {
	t.Helper()

	collected := make(chan prometheus.Metric, 64)
	go func() {
		collector.Collect(collected)
		close(collected)
	}()

	var matching []*dto.Metric
	for metric := range collected {
		var written dto.Metric
		if err := metric.Write(&written); err != nil {
			t.Fatalf("write a collected metric: %v", err)
		}
		for _, label := range written.GetLabel() {
			if label.GetName() == "provider" && label.GetValue() == providerConfig {
				matching = append(matching, &written)
			}
		}
	}
	return matching
}

func soleSeriesFor(t *testing.T, collector prometheus.Collector, providerConfig string) *dto.Metric {
	t.Helper()

	matching := seriesFor(t, collector, providerConfig)
	if len(matching) != 1 {
		t.Fatalf("provider config %q owns %d series, want exactly 1", providerConfig, len(matching))
	}
	return matching[0]
}
