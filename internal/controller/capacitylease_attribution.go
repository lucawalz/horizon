package controller

import (
	"time"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/metrics"
)

const unknownLabel = "unknown"

var unattributed = leaseAttribution{providerConfig: unknownLabel, region: unknownLabel}

type leaseAttribution struct {
	providerConfig string
	region         string
	instanceType   string
}

func attributionOf(lease *v1alpha1.CapacityLease) leaseAttribution {
	return leaseAttribution{
		providerConfig: lease.Status.ProviderConfig,
		region:         lease.Status.Region,
		instanceType:   lease.Status.InstanceType,
	}
}

func latchAttribution(lease *v1alpha1.CapacityLease, attributed leaseAttribution) {
	if lease.Status.ProviderConfig == "" {
		lease.Status.ProviderConfig = attributed.providerConfig
	}
	if lease.Status.Region == "" {
		lease.Status.Region = attributed.region
	}
	if lease.Status.InstanceType == "" {
		lease.Status.InstanceType = attributed.instanceType
	}
}

// a guard against double counting is only durable once the status write that carries it has landed
type metricWrites []func()

func (m *metricWrites) add(write func()) {
	if write == nil {
		return
	}
	*m = append(*m, write)
}

func terminalRecord(attributed leaseAttribution, outcome metrics.Outcome) func() {
	return func() {
		metrics.RecordLeaseTerminal(attributed.providerConfig, attributed.region, outcome)
	}
}

func readyRecord(attributed leaseAttribution, took time.Duration) func() {
	return func() {
		metrics.ObserveLeaseReady(attributed.providerConfig, attributed.region, attributed.instanceType,
			metrics.SelectionPinned, took)
	}
}

func releaseRecord(attributed leaseAttribution, took time.Duration) func() {
	return func() {
		metrics.ObserveLeaseRelease(attributed.providerConfig, attributed.region, attributed.instanceType, took)
	}
}

func releasedInstanceRecord(attributed leaseAttribution, path metrics.Path, createdAt, releasedAt time.Time) func() {
	return func() {
		metrics.RecordInstanceReleased(attributed.providerConfig, attributed.region, attributed.instanceType,
			path, createdAt, releasedAt)
	}
}
