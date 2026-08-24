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
	unbounded bool
}

func latchBackstop(entry *v1alpha1.InstanceStatus, policy v1alpha1.WatchdogPolicy) {
	// the machine boots with this lifetime baked into its cloud-init, so a later edit to the config must not move it
	if entry.BackstopAt != nil || entry.CreatedAt.IsZero() || policy.MaxLifetime.Duration <= 0 {
		return
	}
	entry.BackstopAt = &metav1.Time{Time: wholeSeconds(entry.CreatedAt.Add(policy.MaxLifetime.Duration))}
}

func deriveDeadline(lease *v1alpha1.CapacityLease) leaseDeadline {
	requested := wholeSeconds(lease.Status.AcceptedAt.Add(lease.Spec.Duration.Duration))
	backstop := lease.Status.LifetimeBackstop()
	switch {
	case backstop != nil && backstop.Time.Before(requested):
		return leaseDeadline{at: backstop.Time, requested: requested, clamped: true}
	case backstop != nil:
		return leaseDeadline{at: requested, requested: requested}
	default:
		return leaseDeadline{at: requested, requested: requested, unbounded: holdsInstances(lease)}
	}
}

func holdsInstances(lease *v1alpha1.CapacityLease) bool {
	for _, entry := range lease.Status.Instances {
		if entry.Phase != v1alpha1.InstancePhaseReleased {
			return true
		}
	}
	return false
}

func wholeSeconds(instant time.Time) time.Time {
	return instant.UTC().Truncate(time.Second)
}

func (r *CapacityLeaseReconciler) refreshDeadline(ctx context.Context, lease *v1alpha1.CapacityLease) error {
	if conditionTrue(lease, v1alpha1.ConditionExpired) {
		// re-deriving a deadline the lease has already passed could move it back into the future and provision the lease again
		if lease.Status.ExpiresAt == nil {
			return fmt.Errorf("lease %q reports expired with no deadline recorded", lease.Name)
		}
		return nil
	}
	if !r.applyDeadline(lease) {
		return nil
	}
	return r.writeStatus(ctx, lease)
}

func (r *CapacityLeaseReconciler) applyDeadline(lease *v1alpha1.CapacityLease) bool {
	deadline := deriveDeadline(lease)
	moved := lease.Status.ExpiresAt == nil || !lease.Status.ExpiresAt.Time.Equal(deadline.at)
	lease.Status.ExpiresAt = &metav1.Time{Time: deadline.at}
	recorded := r.recordClamp(lease, deadline)
	return moved || recorded
}

func (r *CapacityLeaseReconciler) recordClamp(lease *v1alpha1.CapacityLease, deadline leaseDeadline) bool {
	switch {
	case deadline.clamped:
		return r.setCondition(lease, v1alpha1.ConditionExpiryClamped, metav1.ConditionTrue, reasonNodeLifetimeBackstop,
			fmt.Sprintf("the deadline is held at %s rather than the requested %s, because a leased machine destroys itself at the lifetime backstop latched when it was created",
				deadline.at.Format(time.RFC3339), deadline.requested.Format(time.RFC3339)))
	case deadline.unbounded:
		return r.setCondition(lease, v1alpha1.ConditionExpiryClamped, metav1.ConditionUnknown, reasonBackstopUnknown,
			"the lease holds instances that record no lifetime backstop, so the deadline cannot be checked against the machines that enforce it")
	default:
		return r.setCondition(lease, v1alpha1.ConditionExpiryClamped, metav1.ConditionFalse, reasonRequestedDuration,
			fmt.Sprintf("the deadline is the requested %s after acceptance", lease.Spec.Duration.Duration))
	}
}
