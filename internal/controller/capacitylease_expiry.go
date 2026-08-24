package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type leaseDeadline struct {
	at        time.Time
	requested time.Time
	clamped   bool
}

func deriveDeadline(lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) leaseDeadline {
	requested := wholeSeconds(lease.Status.AcceptedAt.Time.Add(lease.Spec.Duration.Duration))
	backstop, bounded := nodeLifetimeBackstop(lease, policy)
	if bounded && backstop.Before(requested) {
		return leaseDeadline{at: backstop, requested: requested, clamped: true}
	}
	return leaseDeadline{at: requested, requested: requested}
}

func nodeLifetimeBackstop(lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) (time.Time, bool) {
	if policy.MaxLifetime.Duration <= 0 {
		return time.Time{}, false
	}
	// the node measures maxLifetime from agent start, which the cluster cannot observe, so the earliest instance stands in for it
	var earliest time.Time
	for _, entry := range lease.Status.Instances {
		if entry.Phase == v1alpha1.InstancePhaseReleased || entry.CreatedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || entry.CreatedAt.Time.Before(earliest) {
			earliest = entry.CreatedAt.Time
		}
	}
	if earliest.IsZero() {
		return time.Time{}, false
	}
	return wholeSeconds(earliest.Add(policy.MaxLifetime.Duration)), true
}

func wholeSeconds(instant time.Time) time.Time {
	return instant.UTC().Truncate(time.Second)
}

func (r *CapacityLeaseReconciler) refreshDeadline(ctx context.Context, lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) error {
	// re-deriving a deadline the lease has already passed could move it back into the future and provision the lease again
	if conditionTrue(lease, v1alpha1.ConditionExpired) {
		return nil
	}
	if !r.applyDeadline(lease, policy) {
		return nil
	}
	return r.writeStatus(ctx, lease)
}

func (r *CapacityLeaseReconciler) applyDeadline(lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy) bool {
	deadline := deriveDeadline(lease, policy)
	moved := lease.Status.ExpiresAt == nil || !lease.Status.ExpiresAt.Time.Equal(deadline.at)
	lease.Status.ExpiresAt = &metav1.Time{Time: deadline.at}
	recorded := r.recordClamp(lease, deadline, policy)
	return moved || recorded
}

func (r *CapacityLeaseReconciler) recordClamp(lease *v1alpha1.CapacityLease, deadline leaseDeadline, policy v1alpha1.WatchdogPolicy) bool {
	if !deadline.clamped {
		return r.setCondition(lease, v1alpha1.ConditionExpiryClamped, metav1.ConditionFalse, reasonRequestedDuration,
			fmt.Sprintf("the deadline is the requested %s after acceptance", lease.Spec.Duration.Duration))
	}
	return r.setCondition(lease, v1alpha1.ConditionExpiryClamped, metav1.ConditionTrue, reasonNodeLifetimeBackstop,
		fmt.Sprintf("the deadline is held at %s rather than the requested %s, because watchdog.maxLifetime %s runs out first, anchored to the earliest instance because the node measures that lifetime from agent start",
			deadline.at.Format(time.RFC3339), deadline.requested.Format(time.RFC3339), policy.MaxLifetime.Duration))
}
