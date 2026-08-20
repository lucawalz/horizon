package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	leaseReadySeconds = register(prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "lease_ready_seconds",
		Help:      "Time from lease acceptance to every requested instance joining the cluster.",
		Buckets:   readySecondsBuckets,
	}, []string{labelProvider, labelRegion, labelInstanceType, labelSelection}))

	leaseReleaseSeconds = register(prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "lease_release_seconds",
		Help:      "Time from the start of teardown to the last instance being released.",
		Buckets:   releaseSecondsBuckets,
	}, []string{labelProvider, labelRegion, labelInstanceType, labelPath}))

	leaseTerminalTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "lease_terminal_total",
		Help:      "Capacity leases that reached a terminal outcome.",
	}, []string{labelProvider, labelRegion, labelOutcome}))
)

func ObserveLeaseReady(providerConfig, region, instanceType string, selection Selection, took time.Duration) {
	leaseReadySeconds.WithLabelValues(providerConfig, region, instanceType, string(selection)).Observe(took.Seconds())
}

func ObserveLeaseRelease(providerConfig, region, instanceType string, path Path, took time.Duration) {
	leaseReleaseSeconds.WithLabelValues(providerConfig, region, instanceType, string(path)).Observe(took.Seconds())
}

func RecordLeaseTerminal(providerConfig, region string, outcome Outcome) {
	leaseTerminalTotal.WithLabelValues(providerConfig, region, string(outcome)).Inc()
}
