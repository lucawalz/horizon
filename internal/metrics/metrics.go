// Package metrics owns the horizon metric surface and the recorders that move it.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const namespace = "horizon"

var (
	readySecondsBuckets    = []float64{5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 600}
	releaseSecondsBuckets  = []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 120, 300, 600}
	providerRequestBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30}
)

var registered []prometheus.Collector

func register[C prometheus.Collector](collector C) C {
	registered = append(registered, collector)
	return collector
}

func init() {
	ctrlmetrics.Registry.MustRegister(registered...)
}
