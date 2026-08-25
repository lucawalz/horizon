package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

const (
	BurstCopyLabelKey = "horizon.dev/burst-copy"

	ReasonAutoscalerTargeted = "TargetedByAutoscaler"
	ReasonStatefulSetCopy    = "StatefulSetNotCopyable"
	ReasonBudgetSpansCopy    = "DisruptionBudgetSpansCopy"
	ReasonSpreadSpansCopy    = "TopologySpreadSpansCopy"
	ReasonSelectorUnchanged  = "CopySelectorMatchesOriginal"

	opReplicate         = "replicate"
	opDeleteCopies      = "delete-burst-copies"
	burstCopyInfix      = "-burst-"
	burstCopyHashLength = 8
	// neither a lease UID nor an object name can hold it, so no pair of them digests to the same bytes as another
	burstCopyDigestSeparator = "/"
	maxObjectNameLength      = 253
	minBurstReplicas         = 1
	deploymentAPIKind        = "Deployment"
)

var replicationReasons = map[string]string{
	ReasonAutoscalerTargeted: "a HorizontalPodAutoscaler targets it, and it would read the copy's pods as the workload being over-provisioned and scale the original down; move mode changes no replica count, so it bursts this workload without fighting the autoscaler",
	ReasonStatefulSetCopy:    "a copy of a StatefulSet mints empty volumes rather than carrying the data the workload holds; move mode bursts this workload as it stands",
	ReasonBudgetSpansCopy:    "its PodDisruptionBudget selects the copy's pods as well, so the budget counts pods it was not written for until the lease ends",
	ReasonSelectorUnchanged:  "its own selector already carries this lease's burst-copy label, so the copy's selector would name the original's pods too and the two replica sets would contend over one set of pods",
	ReasonSpreadSpansCopy:    "it spreads its pods over topology domains and refuses to schedule where the spread is unmet, and the copy's pods carry its labels, so they count into its own domains and its next pod can be left Pending; move mode adds no second set of pods, so it bursts this workload without skewing the spread",
}

func ReplicationReasonText(reason string) string {
	if text, named := replicationReasons[reason]; named {
		return text
	}
	return reason
}

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

type WorkloadWarning struct {
	Workload string
	Reasons  []string
}

type ReplicationResult struct {
	Copies               []string
	ReplicatedNamespaces []string
	Skipped              []WorkloadWarning
	Warnings             []WorkloadWarning
}

func (r ReplicationResult) Matched() int {
	return len(r.Copies) + len(r.Skipped)
}

func burstCopyName(original, leaseUID string) string {
	// the digest covers the original's name too, because the trim below can leave two originals of one lease sharing a prefix and so a copy
	digest := sha256.Sum256([]byte(leaseUID + burstCopyDigestSeparator + original))
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

func pinnedToLeaseNodes(original *corev1.Affinity, leaseUID string) *corev1.Affinity {
	// only the node affinity is the lease's to decide, and replacing the whole field would drop a pod anti-affinity that spreads the copy over the rented nodes
	pinned := original.DeepCopy()
	if pinned == nil {
		pinned = &corev1.Affinity{}
	}
	pinned.NodeAffinity = leaseNodeAffinity(leaseUID).NodeAffinity
	return pinned
}

func burstCopy(original *appsv1.Deployment, replication Replication) *appsv1.Deployment {
	lease := replication.Lease
	spec := original.Spec.DeepCopy()
	spec.Replicas = &replication.Replicas
	spec.Selector = burstCopySelector(spec.Selector, lease.Name)
	spec.Template.Labels = labelledAsBurstCopy(spec.Template.Labels, lease.Name)
	spec.Template.Spec.Affinity = pinnedToLeaseNodes(spec.Template.Spec.Affinity, lease.UID)
	spec.Template.Spec.Tolerations = withBurstToleration(spec.Template.Spec.Tolerations, lease.Name)
	// the copy exists to run pods on the leased nodes, and a node selector of the original's names labels none of them carry
	spec.Template.Spec.NodeSelector = nil
	// the copy is the expendable half of the pair, and an inherited priority lets it preempt pods already running on the rented nodes
	spec.Template.Spec.PriorityClassName = ""
	spec.Template.Spec.Priority = nil
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
		replicated, err := replicateNamespace(ctx, kc, namespace, targets.selector, replication)
		result.Copies = append(result.Copies, replicated.copies...)
		result.Skipped = append(result.Skipped, replicated.skipped...)
		result.Warnings = append(result.Warnings, replicated.warnings...)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		result.ReplicatedNamespaces = append(result.ReplicatedNamespaces, namespace)
	}
	return result, failures
}

type namespaceReplication struct {
	copies   []string
	skipped  []WorkloadWarning
	warnings []WorkloadWarning
}

func (n *namespaceReplication) skip(namespace, kind, name, reason string) {
	n.skipped = append(n.skipped, WorkloadWarning{
		Workload: workloadRef(namespace, kind, name),
		Reasons:  []string{reason},
	})
}

func replicateNamespace(ctx context.Context, kc kubernetes.Interface, namespace string, selector labels.Selector, replication Replication) (namespaceReplication, error) {
	var replicated namespaceReplication
	autoscaled, err := autoscaledDeployments(ctx, kc, namespace)
	if err != nil {
		return replicated, err
	}
	budgets, err := disruptionBudgetSelectors(ctx, kc, namespace)
	if err != nil {
		return replicated, err
	}
	if err := skipStatefulSets(ctx, kc, namespace, selector, &replicated); err != nil {
		return replicated, err
	}

	api := kc.AppsV1().Deployments(namespace)
	list, err := api.List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return replicated, fmt.Errorf("%s: list deployments in %q: %w", opReplicate, namespace, err)
	}
	for i := range list.Items {
		original := &list.Items[i]
		if isBurstCopy(original.Labels) {
			continue
		}
		if _, err := workloadSelector(kindDeployment, original.Name, original.Spec.Selector); err != nil {
			return replicated, fmt.Errorf("%s: %w", opReplicate, err)
		}
		// the copy is what does the damage in each of these, so the skip creates nothing rather than warning and proceeding
		if reason := copyHarmsOriginal(original, replication.Lease.Name, autoscaled); reason != "" {
			replicated.skip(namespace, kindDeployment, original.Name, reason)
			continue
		}
		copied := burstCopy(original, replication)
		if matchedByWorkload(copied.Spec.Template.Labels, budgets) {
			replicated.warnings = append(replicated.warnings, WorkloadWarning{
				Workload: workloadRef(namespace, kindDeployment, original.Name),
				Reasons:  []string{ReasonBudgetSpansCopy},
			})
		}
		if _, err := api.Create(ctx, copied, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return replicated, fmt.Errorf("%s: create copy of deployment %q in %q: %w", opReplicate, original.Name, namespace, err)
		}
		replicated.copies = append(replicated.copies, workloadRef(namespace, kindDeployment, copied.Name))
	}
	return replicated, nil
}

func copyHarmsOriginal(original *appsv1.Deployment, leaseName string, autoscaled map[string]bool) string {
	switch {
	case autoscaled[original.Name]:
		return ReasonAutoscalerTargeted
	case enforcesSpread(original.Spec.Template.Spec.TopologySpreadConstraints):
		return ReasonSpreadSpansCopy
	case selectorUnchangedByCopy(original.Spec.Selector, leaseName):
		return ReasonSelectorUnchanged
	}
	return ""
}

func selectorUnchangedByCopy(original *metav1.LabelSelector, leaseName string) bool {
	// the extra label in the copy's selector is the whole of what keeps the two replica sets from contending over one set of pods
	return apiequality.Semantic.DeepEqual(burstCopySelector(original, leaseName), original)
}

func enforcesSpread(constraints []corev1.TopologySpreadConstraint) bool {
	for _, constraint := range constraints {
		// a spread the scheduler only scores on costs the original a preference, never a node, so it is not worth refusing the capacity over
		if constraint.WhenUnsatisfiable == corev1.DoNotSchedule {
			return true
		}
	}
	return false
}

func skipStatefulSets(ctx context.Context, kc kubernetes.Interface, namespace string, selector labels.Selector, replicated *namespaceReplication) error {
	wc := statefulSetClient(kc, namespace, selector)
	targets, err := wc.list(ctx)
	if err != nil {
		return fmt.Errorf("%s: list %s in %q: %w", opReplicate, wc.plural(), namespace, err)
	}
	for _, t := range targets {
		replicated.skip(namespace, kindStatefulSet, t.name, ReasonStatefulSetCopy)
	}
	return nil
}

func autoscaledDeployments(ctx context.Context, kc kubernetes.Interface, namespace string) (map[string]bool, error) {
	list, err := kc.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s: list horizontalpodautoscalers in %q: %w", opReplicate, namespace, err)
	}
	targeted := map[string]bool{}
	for i := range list.Items {
		ref := list.Items[i].Spec.ScaleTargetRef
		if ref.Kind == deploymentAPIKind && scalesApps(ref.APIVersion) {
			targeted[ref.Name] = true
		}
	}
	return targeted, nil
}

func scalesApps(apiVersion string) bool {
	// an unqualified reference is read as the apps group, because the cost of skipping a workload needlessly is far below the cost of copying one an autoscaler governs
	group := schema.FromAPIVersionAndKind(apiVersion, deploymentAPIKind).Group
	return group == appsv1.GroupName || group == ""
}

func disruptionBudgetSelectors(ctx context.Context, kc kubernetes.Interface, namespace string) ([]labels.Selector, error) {
	list, err := kc.PolicyV1().PodDisruptionBudgets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s: list poddisruptionbudgets in %q: %w", opReplicate, namespace, err)
	}
	var selectors []labels.Selector
	for i := range list.Items {
		budget := &list.Items[i]
		// a budget reads its own empty selector as every pod in the namespace, which is the opposite of what a workload's empty selector means
		selector, err := metav1.LabelSelectorAsSelector(budget.Spec.Selector)
		if err != nil {
			return nil, fmt.Errorf("%s: selector of poddisruptionbudget %q in %q: %w", opReplicate, budget.Name, namespace, err)
		}
		selectors = append(selectors, selector)
	}
	return selectors, nil
}

func DeleteBurstCopies(ctx context.Context, kc kubernetes.Interface, copies []string, lease LeaseIdentity) error {
	if err := lease.validate(); err != nil {
		return fmt.Errorf("%s: %w", opDeleteCopies, err)
	}
	targets, err := NamespaceSetOfWorkloads(copies)
	if err != nil {
		return fmt.Errorf("%s: %w", opDeleteCopies, err)
	}
	var failures error
	for _, namespace := range targets.namespaces {
		failures = errors.Join(failures, deleteNamespaceCopies(ctx, kc, namespace, lease))
	}
	return errors.Join(failures, confirmCopiesGone(ctx, kc, copies))
}

func confirmCopiesGone(ctx context.Context, kc kubernetes.Interface, copies []string) error {
	// the delete is label scoped, so a copy stripped of either label falls out of it silently and this list is the only remaining record of it
	var failures error
	for _, ref := range copies {
		namespace, kind, name, err := parseWorkloadRef(ref)
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("%s: %w", opDeleteCopies, err))
			continue
		}
		if kind != kindDeployment {
			failures = errors.Join(failures, fmt.Errorf("%s: %q names a %s, and only a deployment is ever copied", opDeleteCopies, ref, kind))
			continue
		}
		_, err = kc.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			failures = errors.Join(failures, fmt.Errorf("%s: confirm deployment %q in %q is gone: %w", opDeleteCopies, name, namespace, err))
		default:
			failures = errors.Join(failures, fmt.Errorf("%s: burst copy %q outlived the delete of every copy this lease owns", opDeleteCopies, ref))
		}
	}
	return failures
}

func deleteNamespaceCopies(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) error {
	api := kc.AppsV1().Deployments(namespace)
	// both labels are required, so a workload this lease moved rather than copied is never a candidate for deletion
	owned := labels.SelectorFromSet(labels.Set{LeaseUIDLabelKey: lease.UID, BurstCopyLabelKey: lease.Name})
	list, err := api.List(ctx, metav1.ListOptions{LabelSelector: owned.String()})
	if err != nil {
		return fmt.Errorf("%s: list deployments in %q: %w", opDeleteCopies, namespace, err)
	}
	var failures error
	for i := range list.Items {
		name := list.Items[i].Name
		if err := api.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			failures = errors.Join(failures, fmt.Errorf("%s: delete deployment %q in %q: %w", opDeleteCopies, name, namespace, err))
		}
	}
	return failures
}

func isBurstCopy(workloadLabels map[string]string) bool {
	_, copied := workloadLabels[BurstCopyLabelKey]
	return copied
}
