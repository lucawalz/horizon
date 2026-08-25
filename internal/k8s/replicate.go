package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	BurstCopyLabelKey = "horizon.dev/burst-copy"

	burstCopyInfix      = "-burst-"
	burstCopyHashLength = 8
	maxObjectNameLength = 253
)

func burstCopyName(original, leaseUID string) string {
	digest := sha256.Sum256([]byte(leaseUID))
	suffix := burstCopyInfix + hex.EncodeToString(digest[:])[:burstCopyHashLength]
	return trimTo(original, maxObjectNameLength-len(suffix)) + suffix
}

func trimTo(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func labelledAsBurstCopy(original map[string]string, leaseName string) map[string]string {
	copied := maps.Clone(original)
	if copied == nil {
		copied = map[string]string{}
	}
	copied[BurstCopyLabelKey] = leaseName
	return copied
}

func burstCopyLabels(original map[string]string, lease LeaseIdentity) map[string]string {
	copied := labelledAsBurstCopy(original, lease.Name)
	// a copy of a workload another lease moved would otherwise inherit that lease's placement marker along with its labels
	delete(copied, BurstPlacementLabelKey)
	copied[LeaseUIDLabelKey] = lease.UID
	return copied
}

func burstCopySelector(original *metav1.LabelSelector, leaseName string) *metav1.LabelSelector {
	selector := original.DeepCopy()
	if selector.MatchLabels == nil {
		selector.MatchLabels = map[string]string{}
	}
	selector.MatchLabels[BurstCopyLabelKey] = leaseName
	return selector
}

func burstCopy(original *appsv1.Deployment, lease LeaseIdentity, replicas int32) *appsv1.Deployment {
	spec := original.Spec.DeepCopy()
	spec.Replicas = &replicas
	spec.Selector = burstCopySelector(spec.Selector, lease.Name)
	spec.Template.Labels = labelledAsBurstCopy(spec.Template.Labels, lease.Name)
	spec.Template.Spec.Affinity = leaseNodeAffinity(lease.UID)
	spec.Template.Spec.Tolerations = withBurstToleration(spec.Template.Spec.Tolerations, lease.Name)
	// the copy exists to run pods on the leased nodes, and both of these would keep it from ever placing one there
	spec.Template.Spec.NodeSelector = nil
	spec.Paused = false
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      burstCopyName(original.Name, lease.UID),
			Namespace: original.Namespace,
			Labels:    burstCopyLabels(original.Labels, lease),
		},
		Spec: *spec,
	}
}
