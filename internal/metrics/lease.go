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
		Help:      "Time from a lease falling due for teardown to its last instance being confirmed absent.",
		Buckets:   releaseSecondsBuckets,
	}, []string{labelProvider, labelRegion, labelInstanceType}))

	leaseTerminalTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "lease_terminal_total",
		Help:      "Capacity leases that reached a terminal outcome.",
	}, []string{labelProvider, labelRegion, labelOutcome}))
)

func ObserveLeaseReady(providerConfig, region, instanceType string, selection Selection, took time.Duration) {
	leaseReadySeconds.WithLabelValues(providerConfig, region, instanceType, string(selection)).Observe(took.Seconds())
}

func ObserveLeaseRelease(providerConfig, region, instanceType string, took time.Duration) {
	leaseReleaseSeconds.WithLabelValues(providerConfig, region, instanceType).Observe(took.Seconds())
}

func RecordLeaseTerminal(providerConfig, region string, outcome Outcome) {
	leaseTerminalTotal.WithLabelValues(providerConfig, region, string(outcome)).Inc()
}
