package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var providerRequestDurationSeconds = register(prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: namespace,
	Name:      "provider_request_duration_seconds",
	Help:      "Duration of provider API requests, by operation and result.",
	Buckets:   providerRequestBuckets,
}, []string{labelProvider, labelOperation, labelResult}))

func ObserveProviderRequest(providerConfig string, operation Operation, result Result, took time.Duration) {
	providerRequestDurationSeconds.WithLabelValues(providerConfig, string(operation), string(result)).Observe(took.Seconds())
}
