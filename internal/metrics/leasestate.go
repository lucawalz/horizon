package metrics

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type LeaseCondition struct {
	Type   string
	Status metav1.ConditionStatus
}

type LeaseState struct {
	Phases     map[v1alpha1.LeasePhase]int
	Conditions map[LeaseCondition]int
}

// a source must return freshly built maps, because the collector ranges them outside any lock the source holds
type LeaseStateSource func() LeaseState

var (
	leasesDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "leases"),
		"Number of capacity leases in each phase.",
		[]string{labelPhase}, nil,
	)

	leaseStatusConditionDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "lease_status_condition"),
		"Number of capacity leases reporting each status condition.",
		[]string{labelCondition, labelStatus}, nil,
	)
)

type leaseStateCollector struct {
	source atomic.Pointer[LeaseStateSource]
}

var leaseState = register(&leaseStateCollector{})

func SetLeaseStateSource(source LeaseStateSource) error {
	if source == nil {
		return errors.New("metrics: a lease state source is required")
	}
	leaseState.source.Store(&source)
	return nil
}

func (c *leaseStateCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- leasesDesc
	descs <- leaseStatusConditionDesc
}

func (c *leaseStateCollector) Collect(collected chan<- prometheus.Metric) {
	source := c.source.Load()
	if source == nil {
		return
	}

	state, ok := readLeaseState(*source)
	if !ok {
		return
	}

	for phase, count := range state.Phases {
		collected <- prometheus.MustNewConstMetric(leasesDesc, prometheus.GaugeValue, float64(count), string(phase))
	}
	for condition, count := range state.Conditions {
		collected <- prometheus.MustNewConstMetric(leaseStatusConditionDesc, prometheus.GaugeValue, float64(count), condition.Type, string(condition.Status))
	}
}

// a scrape runs on a worker goroutine the registry never recovers, so an unguarded source would end the process
func readLeaseState(source LeaseStateSource) (state LeaseState, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			state, ok = LeaseState{}, false
			ctrl.Log.WithName("metrics").Error(fmt.Errorf("%v", recovered), "read the lease state for a scrape")
		}
	}()
	return source(), true
}
