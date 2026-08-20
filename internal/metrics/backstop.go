package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	watchdogRenewalsTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "watchdog_renewals_total",
		Help:      "Dead man's switch renewals written to burst instances, by result.",
	}, []string{labelProvider, labelResult}))

	orphanInstancesDeletedTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "orphan_instances_deleted_total",
		Help:      "Instances deleted by the orphan sweep because no lease claimed them.",
	}, []string{labelProvider, labelRegion}))
)

func RecordWatchdogRenewal(providerConfig string, result Result) {
	watchdogRenewalsTotal.WithLabelValues(providerConfig, string(result)).Inc()
}

func RecordOrphanInstanceDeleted(providerConfig, region string) {
	orphanInstancesDeletedTotal.WithLabelValues(providerConfig, region).Inc()
}
