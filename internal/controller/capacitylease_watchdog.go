package controller

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

type watchdogRenewal struct {
	deadline time.Time
	policy   v1alpha1.WatchdogPolicy
	now      time.Time
}

func newWatchdogRenewal(lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy, now time.Time) watchdogRenewal {
	return watchdogRenewal{
		deadline: watchdogDeadline(lease, policy, now),
		policy:   policy,
		now:      now,
	}
}

func (w watchdogRenewal) annotationsFor(node *corev1.Node) (map[string]string, bool) {
	value := node.Annotations[provider.WatchdogDeadlineAnnotationKey]
	renewed := shouldRenew(value, w.deadline, w.policy, w.now)
	if renewed {
		value = provider.FormatExpiry(w.deadline)
	}
	return map[string]string{provider.WatchdogDeadlineAnnotationKey: value}, renewed
}

func watchdogDeadline(lease *v1alpha1.CapacityLease, policy v1alpha1.WatchdogPolicy, now time.Time) time.Time {
	deadline := now.Add(policy.RenewInterval.Duration + policy.Slack.Duration)
	if lease.Status.ExpiresAt != nil && lease.Status.ExpiresAt.Time.Before(deadline) {
		deadline = lease.Status.ExpiresAt.Time
	}
	return deadline.UTC().Truncate(time.Second)
}

func shouldRenew(annotated string, deadline time.Time, policy v1alpha1.WatchdogPolicy, now time.Time) bool {
	current, readable := provider.ParseExpiryValue(annotated)
	if !readable || deadline.Before(current) {
		return true
	}
	return !now.Before(current.Add(-policy.Slack.Duration))
}

func recordWatchdogDeadline(lease *v1alpha1.CapacityLease, deadline time.Time) bool {
	if lease.Status.WatchdogDeadline != nil && lease.Status.WatchdogDeadline.Time.Equal(deadline) {
		return false
	}
	lease.Status.WatchdogDeadline = &metav1.Time{Time: deadline}
	return true
}
