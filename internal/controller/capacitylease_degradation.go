package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type degradation struct {
	reason  string
	message string
}

func (d *degradation) observe(reason, message string) {
	if d.reason != "" {
		return
	}
	d.reason = reason
	d.message = message
}

// a pass that finished every stage with no work left over is the only evidence that the lease recovered
func (r *CapacityLeaseReconciler) resolveDegraded(ctx context.Context, lease *v1alpha1.CapacityLease, degraded degradation, healthy bool) error {
	var changed bool
	switch {
	case degraded.reason != "":
		changed = r.setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionTrue, degraded.reason, degraded.message)
	case healthy && meta.FindStatusCondition(lease.Status.Conditions, v1alpha1.ConditionDegraded) != nil:
		changed = r.setCondition(lease, v1alpha1.ConditionDegraded, metav1.ConditionFalse, reasonRecovered,
			"the lease completed a pass with no degradation observed")
	}
	if !changed {
		return nil
	}
	return r.writeStatus(ctx, lease)
}
