package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	BurstCopyLabelKey = "horizon.dev/burst-copy"

	opReplicate         = "replicate"
	burstCopyInfix      = "-burst-"
	burstCopyHashLength = 8
	maxObjectNameLength = 253
	minBurstReplicas    = 1
)

type Replication struct {
	Lease    LeaseIdentity
	Replicas int32
	Owner    metav1.OwnerReference
}

func (r Replication) validate() error {
	if err := r.Lease.validate(); err != nil {
		return err
	}
	if r.Replicas < minBurstReplicas {
		return fmt.Errorf("burst replicas must be at least %d", minBurstReplicas)
	}
	if r.Owner.Name == "" || r.Owner.UID == "" {
		return fmt.Errorf("owner reference must name the lease")
	}
	return nil
}

type ReplicationResult struct {
	Copies               []string
	ReplicatedNamespaces []string
}

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

func burstCopy(original *appsv1.Deployment, replication Replication) *appsv1.Deployment {
	lease := replication.Lease
	spec := original.Spec.DeepCopy()
	spec.Replicas = &replication.Replicas
	spec.Selector = burstCopySelector(spec.Selector, lease.Name)
	spec.Template.Labels = labelledAsBurstCopy(spec.Template.Labels, lease.Name)
	spec.Template.Spec.Affinity = leaseNodeAffinity(lease.UID)
	spec.Template.Spec.Tolerations = withBurstToleration(spec.Template.Spec.Tolerations, lease.Name)
	// the copy exists to run pods on the leased nodes, and both of these would keep it from ever placing one there
	spec.Template.Spec.NodeSelector = nil
	spec.Paused = false
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            burstCopyName(original.Name, lease.UID),
			Namespace:       original.Namespace,
			Labels:          burstCopyLabels(original.Labels, lease),
			OwnerReferences: []metav1.OwnerReference{replication.Owner},
		},
		Spec: *spec,
	}
}

func Replicate(ctx context.Context, kc kubernetes.Interface, targets TargetSet, replication Replication) (ReplicationResult, error) {
	if err := replication.validate(); err != nil {
		return ReplicationResult{}, fmt.Errorf("%s: %w", opReplicate, err)
	}
	if err := requireLeaseNode(ctx, kc, replication.Lease.UID, opReplicate); err != nil {
		return ReplicationResult{}, err
	}

	var result ReplicationResult
	var failures error
	for _, namespace := range targets.namespaces {
		copies, err := replicateNamespace(ctx, kc, namespace, targets.selector, replication)
		result.Copies = append(result.Copies, copies...)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		result.ReplicatedNamespaces = append(result.ReplicatedNamespaces, namespace)
	}
	return result, failures
}

func replicateNamespace(ctx context.Context, kc kubernetes.Interface, namespace string, selector labels.Selector, replication Replication) ([]string, error) {
	api := kc.AppsV1().Deployments(namespace)
	list, err := api.List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, fmt.Errorf("%s: list deployments in %q: %w", opReplicate, namespace, err)
	}

	var copies []string
	for i := range list.Items {
		original := &list.Items[i]
		if isBurstCopy(original.Labels) {
			continue
		}
		if _, err := workloadSelector(kindDeployment, original.Name, original.Spec.Selector); err != nil {
			return copies, fmt.Errorf("%s: %w", opReplicate, err)
		}
		copied := burstCopy(original, replication)
		if _, err := api.Create(ctx, copied, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return copies, fmt.Errorf("%s: create copy of deployment %q in %q: %w", opReplicate, original.Name, namespace, err)
		}
		copies = append(copies, workloadRef(namespace, kindDeployment, copied.Name))
	}
	return copies, nil
}

func isBurstCopy(workloadLabels map[string]string) bool {
	_, copied := workloadLabels[BurstCopyLabelKey]
	return copied
}
