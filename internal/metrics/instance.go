package metrics

import (
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const billingIncrement = time.Hour

var (
	instanceReleasedTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_released_total",
		Help:      "Instances confirmed absent at the provider, by the path that released them.",
	}, []string{labelProvider, labelRegion, labelInstanceType, labelPath}))

	instanceSecondsTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_seconds_total",
		Help:      "Wall clock seconds instances existed at the provider.",
	}, []string{labelProvider, labelRegion, labelInstanceType}))

	instanceBilledHoursTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_billed_hours_total",
		Help:      "Hours instances are billed for, rounded up to the whole billing increment.",
	}, []string{labelProvider, labelRegion, labelInstanceType}))

	instanceLifetimeUnknownTotal = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_lifetime_unknown_total",
		Help:      "Released instances whose creation instant was unknown, so their cost is missing from the two cost counters.",
	}, []string{labelProvider, labelRegion, labelInstanceType}))
)

func RecordInstanceReleased(providerConfig, region, instanceType string, path Path, createdAt, releasedAt time.Time) {
	instanceReleasedTotal.WithLabelValues(providerConfig, region, instanceType, string(path)).Inc()

	if createdAt.IsZero() {
		instanceLifetimeUnknownTotal.WithLabelValues(providerConfig, region, instanceType).Inc()
		return
	}

	// clock skew can hand back a negative lifetime, and a counter panics on a negative add
	billable := max(releasedAt.Sub(createdAt), 0)
	instanceSecondsTotal.WithLabelValues(providerConfig, region, instanceType).Add(billable.Seconds())
	instanceBilledHoursTotal.WithLabelValues(providerConfig, region, instanceType).Add(billedHours(billable))
}

func billedHours(lifetime time.Duration) float64 {
	increments := math.Ceil(float64(lifetime) / float64(billingIncrement))
	return increments * billingIncrement.Hours()
}
