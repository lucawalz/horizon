package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	instanceTypePriceEstimate = register(prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "instance_type_price_estimate",
		Help:      "Hourly rate the provider quotes for an instance type, in the provider's own currency.",
	}, []string{labelProvider, labelRegion, labelInstanceType}))

	instanceTypeCPUCores = register(prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "instance_type_cpu_cores",
		Help:      "CPU cores an instance type offers.",
	}, []string{labelProvider, labelInstanceType}))

	instanceTypeMemoryBytes = register(prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "instance_type_memory_bytes",
		Help:      "Memory an instance type offers, in bytes.",
	}, []string{labelProvider, labelInstanceType}))

	instanceTypeSelectedTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_type_selected_total",
		Help:      "Instance types chosen for a lease, by the strategy that chose them.",
	}, []string{labelProvider, labelRegion, labelInstanceType, labelStrategy}))

	instanceTypeSelectionFailedTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_type_selection_failed_total",
		Help:      "Leases rejected because no instance type could be chosen.",
	}, []string{labelProvider, labelRegion, labelStrategy, labelReason}))

	catalogueAgeSeconds = register(prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "instance_type_catalogue_age_seconds",
		Help:      "Age of the cached instance type catalogue for a provider config.",
	}, []string{labelProvider}))

	catalogueRefreshTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_type_catalogue_refresh_total",
		Help:      "Instance type catalogue refresh attempts, by result.",
	}, []string{labelProvider, labelResult}))
)

func SetInstanceTypePrice(providerConfig, region, instanceType string, hourlyRate float64) {
	instanceTypePriceEstimate.WithLabelValues(providerConfig, region, instanceType).Set(hourlyRate)
}

func SetInstanceTypeCapacity(providerConfig, instanceType string, cores int, memoryBytes int64) {
	instanceTypeCPUCores.WithLabelValues(providerConfig, instanceType).Set(float64(cores))
	instanceTypeMemoryBytes.WithLabelValues(providerConfig, instanceType).Set(float64(memoryBytes))
}

func SetCatalogueAge(providerConfig string, age time.Duration) {
	catalogueAgeSeconds.WithLabelValues(providerConfig).Set(age.Seconds())
}

// absence, rather than zero, is how an unfilled catalogue is reported
func ForgetProviderCatalogue(providerConfig string) {
	owned := prometheus.Labels{labelProvider: providerConfig}
	instanceTypePriceEstimate.DeletePartialMatch(owned)
	instanceTypeCPUCores.DeletePartialMatch(owned)
	instanceTypeMemoryBytes.DeletePartialMatch(owned)
	catalogueAgeSeconds.DeletePartialMatch(owned)
}

func RecordCatalogueRefresh(providerConfig string, result Result) {
	catalogueRefreshTotal.WithLabelValues(providerConfig, string(result)).Inc()
}

func RecordInstanceTypeSelected(providerConfig, region, instanceType string, strategy Selection) {
	instanceTypeSelectedTotal.WithLabelValues(providerConfig, region, instanceType, string(strategy)).Inc()
}

func RecordSelectionFailed(providerConfig, region string, strategy Selection, reason Reason) {
	instanceTypeSelectionFailedTotal.WithLabelValues(providerConfig, region, string(strategy), string(reason)).Inc()
}
