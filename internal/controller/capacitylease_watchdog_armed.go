package controller

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const watchdogArmedStalenessRenewIntervalMultiple = 3

func watchdogArmedStalenessWindow(policy v1alpha1.WatchdogPolicy) time.Duration {
	return policy.RenewInterval.Duration * watchdogArmedStalenessRenewIntervalMultiple
}

func watchdogArmedFresh(node *corev1.Node, now time.Time, staleAfter time.Duration) bool {
	raw, annotated := node.Annotations[provider.WatchdogArmedAnnotationKey]
	if !annotated {
		return false
	}
	armedAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false
	}
	return now.Sub(armedAt) < staleAfter
}

func (r *CapacityLeaseReconciler) reconcileWatchdogArmed(lease *v1alpha1.CapacityLease, nodes []corev1.Node, policy v1alpha1.WatchdogPolicy) bool {
	staleAfter := watchdogArmedStalenessWindow(policy)
	now := r.now()

	joined := 0
	var unarmed []string
	for i := range lease.Status.Instances {
		entry := &lease.Status.Instances[i]
		if entry.Phase != v1alpha1.InstancePhaseJoined {
			continue
		}
		joined++
		node := matchNode(nodes, entry)
		if node == nil || !watchdogArmedFresh(node, now, staleAfter) {
			unarmed = append(unarmed, entry.Name)
		}
	}
	if joined == 0 {
		return false
	}

	if len(unarmed) == 0 {
		return setCondition(lease, v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue, reasonWatchdogArmed,
			"every joined node reports an armed watchdog")
	}

	wasFalse := meta.IsStatusConditionFalse(lease.Status.Conditions, v1alpha1.ConditionWatchdogArmed)
	message := fmt.Sprintf("watchdog not confirmed armed on: %s", strings.Join(unarmed, ", "))
	changed := setCondition(lease, v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse, reasonWatchdogUnarmed, message)
	if changed && !wasFalse {
		r.Recorder.Event(lease, corev1.EventTypeWarning, reasonWatchdogUnarmed, message)
	}
	return changed
}
