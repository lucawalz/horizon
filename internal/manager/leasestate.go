package manager

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/metrics"
)

const (
	leaseStateReadTimeout = 5 * time.Second
	unclassifiedPhase     = v1alpha1.LeasePhase("unknown")
)

var countedPhases = map[v1alpha1.LeasePhase]bool{
	v1alpha1.LeasePhasePending:      true,
	v1alpha1.LeasePhaseProvisioning: true,
	v1alpha1.LeasePhaseActive:       true,
	v1alpha1.LeasePhaseExpiring:     true,
	v1alpha1.LeasePhaseReleased:     true,
	v1alpha1.LeasePhaseDegraded:     true,
}

var countedConditions = map[string]bool{
	v1alpha1.ConditionAccepted:         true,
	v1alpha1.ConditionInstancesReady:   true,
	v1alpha1.ConditionWatchdogArmed:    true,
	v1alpha1.ConditionWorkloadMigrated: true,
	v1alpha1.ConditionExpired:          true,
	v1alpha1.ConditionReleased:         true,
	v1alpha1.ConditionDegraded:         true,
}

// an unsynced informer waits on the context it is handed, and a scrape must not wait with it
func leaseStateSource(reader client.Reader, budget time.Duration) metrics.LeaseStateSource {
	log := ctrl.Log.WithName("leasestate")
	return func() metrics.LeaseState {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()

		var leases v1alpha1.CapacityLeaseList
		if err := reader.List(ctx, &leases); err != nil {
			log.Error(err, "list capacity leases for a scrape")
			return metrics.LeaseState{}
		}
		return countLeases(leases.Items)
	}
}

// the collector ranges these maps outside every lock, so each scrape is handed maps nothing else holds
func countLeases(leases []v1alpha1.CapacityLease) metrics.LeaseState {
	state := metrics.LeaseState{
		Phases:     make(map[v1alpha1.LeasePhase]int, len(countedPhases)),
		Conditions: make(map[metrics.LeaseCondition]int, len(countedConditions)),
	}
	for i := range leases {
		state.Phases[countedPhase(leases[i].Status.Phase)]++
		for _, condition := range leases[i].Status.Conditions {
			if !countedConditions[condition.Type] {
				continue
			}
			state.Conditions[metrics.LeaseCondition{Type: condition.Type, Status: condition.Status}]++
		}
	}
	return state
}

// the CRD constrains neither the phase nor a condition type, so only the declared ones may reach a label
func countedPhase(phase v1alpha1.LeasePhase) v1alpha1.LeasePhase {
	if countedPhases[phase] {
		return phase
	}
	return unclassifiedPhase
}
