package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	LeaseUIDLabelKey          = provider.LeaseUIDLabelKey
	BurstTaintKey             = "horizon.dev/burst"
	PrePlacementAnnotationKey = "horizon.dev/pre-burst-placement"
	BurstPlacementLabelKey    = "horizon.dev/burst-placement"
	BurstPlacementLabelValue  = "true"
	burstTaintEffect          = corev1.TaintEffectNoSchedule
	kindDeployment            = "deployment"
	kindStatefulSet           = "statefulset"
	strategicPatchDirective   = "$patch"
	strategicPatchReplace     = "replace"
	opMigrate                 = "migrate"
	opRestore                 = "restore-placement"
	// a workload reference keys a status list-map, so one name per namespace would otherwise collide across a target set
	workloadRefSeparator = "/"
	// one reconcile worker serves every lease, so one window capped far below the grace covers a whole call and the requeue finishes the job
	maxEvictRetryWindow    = 2 * time.Second
	evictAttemptsPerWindow = 4
)

type evictionRetry struct {
	until time.Time
	delay time.Duration
}

func retryWithin(budget time.Duration) evictionRetry {
	window := min(budget, maxEvictRetryWindow)
	return evictionRetry{until: time.Now().Add(window), delay: window / evictAttemptsPerWindow}
}

func noRetry() evictionRetry {
	return evictionRetry{}
}

func (r evictionRetry) allows() bool {
	return r.delay > 0 && time.Now().Add(r.delay).Before(r.until)
}

var namespaceNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace: empty")
	}
	if !namespaceNameRegex.MatchString(ns) {
		return fmt.Errorf("namespace: %q does not match k8s namespace name regex", ns)
	}
	return nil
}

type savedPlacement struct {
	Affinity     *corev1.Affinity    `json:"affinity,omitempty"`
	Tolerations  []corev1.Toleration `json:"tolerations,omitempty"`
	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
}

type metadataPatch struct {
	Annotations map[string]*string `json:"annotations,omitempty"`
	Labels      map[string]*string `json:"labels,omitempty"`
}

type metadataOnlyPatch struct {
	Metadata metadataPatch `json:"metadata"`
}

type podPlacementPatch struct {
	Affinity    any                 `json:"affinity"`
	Tolerations []corev1.Toleration `json:"tolerations"`
	// an omitted node selector leaves the workload's own alone, which is what an annotation predating this field needs on restore
	NodeSelector map[string]*string `json:"nodeSelector,omitempty"`
}

type placementPatch struct {
	Metadata metadataPatch `json:"metadata"`
	Spec     struct {
		Template struct {
			Spec podPlacementPatch `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func buildPlacementPatch(meta metadataPatch, placement podPlacementPatch) ([]byte, error) {
	p := placementPatch{Metadata: meta}
	p.Spec.Template.Spec = placement
	return json.Marshal(p)
}

func nodeSelectorPatch(wanted, current map[string]string) map[string]*string {
	fields := make(map[string]*string, len(current)+len(wanted))
	// strategic merge ignores the replace directive on a field the object no longer carries, so every key is dropped by name instead
	for key := range current {
		fields[key] = nil
	}
	for key, value := range wanted {
		fields[key] = &value
	}
	return fields
}

func replacingAffinity(a *corev1.Affinity, kind, name string) (map[string]any, error) {
	if a == nil {
		return nil, nil
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("restore-placement: marshal affinity for %s %q: %w", kind, name, err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("restore-placement: decode affinity for %s %q: %w", kind, name, err)
	}
	fields[strategicPatchDirective] = strategicPatchReplace
	return fields, nil
}

func savedPlacementMarkers(placement, leaseUID string) metadataPatch {
	value := BurstPlacementLabelValue
	return metadataPatch{
		Annotations: map[string]*string{PrePlacementAnnotationKey: &placement},
		Labels: map[string]*string{
			BurstPlacementLabelKey: &value,
			LeaseUIDLabelKey:       &leaseUID,
		},
	}
}

func buildOwnershipPatch(leaseUID string) ([]byte, error) {
	return json.Marshal(metadataOnlyPatch{Metadata: metadataPatch{
		Labels: map[string]*string{LeaseUIDLabelKey: &leaseUID},
	}})
}

func clearedPlacementMarkers() metadataPatch {
	return metadataPatch{
		Annotations: map[string]*string{PrePlacementAnnotationKey: nil},
		Labels: map[string]*string{
			BurstPlacementLabelKey: nil,
			LeaseUIDLabelKey:       nil,
		},
	}
}

func placementOwner(workloadLabels map[string]string) (string, bool) {
	uid, ok := workloadLabels[LeaseUIDLabelKey]
	return uid, ok && uid != ""
}

type LeaseIdentity struct {
	UID  string
	Name string
}

func (l LeaseIdentity) validate() error {
	if l.UID == "" {
		return fmt.Errorf("lease uid must not be empty")
	}
	if l.Name == "" {
		return fmt.Errorf("lease name must not be empty")
	}
	return nil
}

func burstToleration(leaseName string) corev1.Toleration {
	return corev1.Toleration{
		Key:      BurstTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    leaseName,
		Effect:   burstTaintEffect,
	}
}

func leaseNodeAffinity(leaseUID string) *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      LeaseUIDLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{leaseUID},
					}},
				}},
			},
		},
	}
}

type workloadTarget struct {
	name           string
	annotations    map[string]string
	labels         map[string]string
	podSpec        *corev1.PodSpec
	selector       labels.Selector
	rolloutReason  string
	strategyReason string
	replicas       int
}

func (t workloadTarget) selfRolls() bool {
	return t.rolloutReason == ""
}

func workloadSelector(kind, name string, sel *metav1.LabelSelector) (labels.Selector, error) {
	if sel == nil || len(sel.MatchLabels)+len(sel.MatchExpressions) == 0 {
		return nil, fmt.Errorf("selector for %s %q: empty selector cannot name the pods of this workload", kind, name)
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return nil, fmt.Errorf("selector for %s %q: %w", kind, name, err)
	}
	return selector, nil
}

type workloadClient struct {
	kind      string
	namespace string
	list      func(ctx context.Context) ([]workloadTarget, error)
	patch     func(ctx context.Context, name string, data []byte) error
}

func (wc workloadClient) plural() string {
	return wc.kind + "s"
}

func (wc workloadClient) ref(name string) string {
	return workloadRef(wc.namespace, wc.kind, name)
}

func workloadRef(namespace, kind, name string) string {
	return namespace + workloadRefSeparator + kind + workloadRefSeparator + name
}

func parseWorkloadRef(ref string) (namespace, kind, name string, err error) {
	parts := strings.Split(ref, workloadRefSeparator)
	if len(parts) != 3 || slices.Contains(parts, "") {
		return "", "", "", fmt.Errorf("workload reference %q does not read as namespace/kind/name", ref)
	}
	return parts[0], parts[1], parts[2], nil
}

func NamespaceSetOfWorkloads(refs []string) (TargetSet, error) {
	var namespaces []string
	for _, ref := range refs {
		namespace, _, _, err := parseWorkloadRef(ref)
		if err != nil {
			return TargetSet{}, fmt.Errorf("target set: %w", err)
		}
		if !slices.Contains(namespaces, namespace) {
			namespaces = append(namespaces, namespace)
		}
	}
	return NewNamespaceSet(namespaces)
}

func deploymentClient(kc kubernetes.Interface, namespace string, selector labels.Selector) workloadClient {
	api := kc.AppsV1().Deployments(namespace)
	return workloadClient{
		kind:      kindDeployment,
		namespace: namespace,
		list: func(ctx context.Context) ([]workloadTarget, error) {
			list, err := api.List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
			if err != nil {
				return nil, err
			}
			targets := make([]workloadTarget, 0, len(list.Items))
			for i := range list.Items {
				item := &list.Items[i]
				selector, err := workloadSelector(kindDeployment, item.Name, item.Spec.Selector)
				if err != nil {
					return nil, err
				}
				targets = append(targets, workloadTarget{
					name:           item.Name,
					annotations:    item.Annotations,
					labels:         item.Labels,
					podSpec:        &item.Spec.Template.Spec,
					selector:       selector,
					rolloutReason:  deploymentRolloutReason(item.Spec),
					strategyReason: deploymentStrategyReason(item.Spec),
					replicas:       desiredReplicas(item.Spec.Replicas),
				})
			}
			return targets, nil
		},
		patch: func(ctx context.Context, name string, data []byte) error {
			_, err := api.Patch(ctx, name, types.StrategicMergePatchType, data, metav1.PatchOptions{})
			return err
		},
	}
}

func statefulSetClient(kc kubernetes.Interface, namespace string, selector labels.Selector) workloadClient {
	api := kc.AppsV1().StatefulSets(namespace)
	return workloadClient{
		kind:      kindStatefulSet,
		namespace: namespace,
		list: func(ctx context.Context) ([]workloadTarget, error) {
			list, err := api.List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
			if err != nil {
				return nil, err
			}
			targets := make([]workloadTarget, 0, len(list.Items))
			for i := range list.Items {
				item := &list.Items[i]
				selector, err := workloadSelector(kindStatefulSet, item.Name, item.Spec.Selector)
				if err != nil {
					return nil, err
				}
				targets = append(targets, workloadTarget{
					name:          item.Name,
					annotations:   item.Annotations,
					labels:        item.Labels,
					podSpec:       &item.Spec.Template.Spec,
					selector:      selector,
					rolloutReason: statefulSetRolloutReason(item.Spec.UpdateStrategy),
					replicas:      desiredReplicas(item.Spec.Replicas),
				})
			}
			return targets, nil
		},
		patch: func(ctx context.Context, name string, data []byte) error {
			_, err := api.Patch(ctx, name, types.StrategicMergePatchType, data, metav1.PatchOptions{})
			return err
		},
	}
}

func workloadClients(kc kubernetes.Interface, namespace string, selector labels.Selector) []workloadClient {
	return []workloadClient{deploymentClient(kc, namespace, selector), statefulSetClient(kc, namespace, selector)}
}

type TargetSet struct {
	namespaces []string
	selector   labels.Selector
}

func NewNamespaceSet(namespaces []string) (TargetSet, error) {
	if len(namespaces) == 0 {
		return TargetSet{}, fmt.Errorf("target set: names no namespace")
	}
	for _, namespace := range namespaces {
		if err := ValidateNamespace(namespace); err != nil {
			return TargetSet{}, fmt.Errorf("target set: %w", err)
		}
	}
	return TargetSet{namespaces: slices.Clone(namespaces), selector: labels.Everything()}, nil
}

func NewTargetSet(namespaces []string, selector *metav1.LabelSelector) (TargetSet, error) {
	set, err := NewNamespaceSet(namespaces)
	if err != nil {
		return TargetSet{}, err
	}
	if selector == nil {
		return set, nil
	}
	compiled, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return TargetSet{}, fmt.Errorf("target set: workload selector: %w", err)
	}
	set.selector = compiled
	return set, nil
}

type MigrationResult struct {
	Workloads          []string
	MigratedNamespaces []string
}

func Migrate(ctx context.Context, kc kubernetes.Interface, targets TargetSet, lease LeaseIdentity, evictionBudget time.Duration) (MigrationResult, error) {
	if err := lease.validate(); err != nil {
		return MigrationResult{}, fmt.Errorf("migrate: %w", err)
	}

	if err := requireLeaseNode(ctx, kc, lease.UID, opMigrate); err != nil {
		return MigrationResult{}, err
	}

	var result MigrationResult
	var failures error
	retry := retryWithin(evictionBudget)
	for _, namespace := range targets.namespaces {
		moved, err := migrateNamespace(ctx, kc, namespace, targets.selector, lease, retry)
		result.Workloads = append(result.Workloads, moved...)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		result.MigratedNamespaces = append(result.MigratedNamespaces, namespace)
	}
	return result, failures
}

func migrateNamespace(ctx context.Context, kc kubernetes.Interface, namespace string, selector labels.Selector, lease LeaseIdentity, retry evictionRetry) ([]string, error) {
	var onBurst []string
	var notSelfRolling []labels.Selector
	for _, wc := range workloadClients(kc, namespace, selector) {
		names, selectors, err := migrateWorkloads(ctx, wc, lease)
		onBurst = append(onBurst, names...)
		notSelfRolling = append(notSelfRolling, selectors...)
		if err != nil {
			return onBurst, err
		}
	}
	if err := evictStrandedPods(ctx, kc, namespace, notSelfRolling, lease, retry); err != nil {
		return onBurst, err
	}
	return onBurst, nil
}

func migrateWorkloads(ctx context.Context, wc workloadClient, lease LeaseIdentity) (onBurst []string, notSelfRolling []labels.Selector, err error) {
	targets, err := wc.list(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("migrate: list %s in %q: %w", wc.plural(), wc.namespace, err)
	}
	for _, t := range targets {
		if isBurstCopy(t.labels) {
			continue
		}
		if _, alreadyMoved := t.annotations[PrePlacementAnnotationKey]; alreadyMoved {
			owner, owned := placementOwner(t.labels)
			if owned && owner != lease.UID {
				continue
			}
			// a marker written before ownership was recorded names no lease, and only stamping one makes the workload countable at the readiness gate
			if !owned {
				if err := stampOwner(ctx, wc, t.name, lease.UID); err != nil {
					return onBurst, notSelfRolling, err
				}
			}
		} else {
			patchData, err := buildMigratePatch(t, lease)
			if err != nil {
				return onBurst, notSelfRolling, err
			}
			if err := wc.patch(ctx, t.name, patchData); err != nil {
				return onBurst, notSelfRolling, fmt.Errorf("migrate: patch %s %q: %w", wc.kind, t.name, err)
			}
		}
		onBurst = append(onBurst, wc.ref(t.name))
		if !t.selfRolls() {
			notSelfRolling = append(notSelfRolling, t.selector)
		}
	}
	return onBurst, notSelfRolling, nil
}

func stampOwner(ctx context.Context, wc workloadClient, name, leaseUID string) error {
	patchData, err := buildOwnershipPatch(leaseUID)
	if err != nil {
		return fmt.Errorf("migrate: marshal owner patch for %s %q: %w", wc.kind, name, err)
	}
	if err := wc.patch(ctx, name, patchData); err != nil {
		return fmt.Errorf("migrate: stamp owner on %s %q: %w", wc.kind, name, err)
	}
	return nil
}

func buildMigratePatch(t workloadTarget, lease LeaseIdentity) ([]byte, error) {
	placement, err := marshalPlacement(t.podSpec, t.name)
	if err != nil {
		return nil, err
	}
	moved := podPlacementPatch{
		Affinity:    leaseNodeAffinity(lease.UID),
		Tolerations: withBurstToleration(t.podSpec.Tolerations, lease.Name),
	}
	if len(t.podSpec.NodeSelector) > 0 {
		moved.NodeSelector = nodeSelectorPatch(nil, t.podSpec.NodeSelector)
	}
	patchData, err := buildPlacementPatch(savedPlacementMarkers(placement, lease.UID), moved)
	if err != nil {
		return nil, fmt.Errorf("migrate: marshal patch for %q: %w", t.name, err)
	}
	return patchData, nil
}

func withBurstToleration(existing []corev1.Toleration, leaseName string) []corev1.Toleration {
	for _, t := range existing {
		if t.Key == BurstTaintKey && t.Effect == burstTaintEffect &&
			t.Operator == corev1.TolerationOpEqual && t.Value == leaseName {
			return existing
		}
	}
	return append(append([]corev1.Toleration{}, existing...), burstToleration(leaseName))
}

func marshalPlacement(podSpec *corev1.PodSpec, name string) (string, error) {
	data, err := json.Marshal(savedPlacement{
		Affinity:     podSpec.Affinity,
		Tolerations:  podSpec.Tolerations,
		NodeSelector: podSpec.NodeSelector,
	})
	if err != nil {
		return "", fmt.Errorf("migrate: marshal placement for %q: %w", name, err)
	}
	return string(data), nil
}

func evictablePods(pods []corev1.Pod, selectors []labels.Selector) []*corev1.Pod {
	var evictable []*corev1.Pod
	for i := range pods {
		pod := &pods[i]
		if isDaemonSetPod(pod) || isTerminalPod(pod) || pod.DeletionTimestamp != nil {
			continue
		}
		if !matchedByWorkload(pod.Labels, selectors) {
			continue
		}
		evictable = append(evictable, pod)
	}
	return evictable
}

func matchedByWorkload(podLabels map[string]string, selectors []labels.Selector) bool {
	set := labels.Set(podLabels)
	for _, sel := range selectors {
		if sel.Matches(set) {
			return true
		}
	}
	return false
}

func workloadPods(ctx context.Context, kc kubernetes.Interface, namespace string, selectors []labels.Selector, op string) ([]*corev1.Pod, error) {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s: list pods in %q: %w", op, namespace, err)
	}
	return evictablePods(pods.Items, selectors), nil
}

func evictWorkloadPods(ctx context.Context, kc kubernetes.Interface, namespace string, selectors []labels.Selector, op string) error {
	pods, err := workloadPods(ctx, kc, namespace, selectors, op)
	if err != nil {
		return err
	}
	return evictPods(ctx, kc, pods, op, noRetry())
}

func evictStrandedPods(ctx context.Context, kc kubernetes.Interface, namespace string, selectors []labels.Selector, lease LeaseIdentity, retry evictionRetry) error {
	if len(selectors) == 0 {
		return nil
	}
	pods, err := workloadPods(ctx, kc, namespace, selectors, opMigrate)
	if err != nil {
		return err
	}
	burstNodes, err := leaseNodes(ctx, kc, lease.UID)
	if err != nil {
		return fmt.Errorf("%s: list nodes: %w", opMigrate, err)
	}

	var stranded []*corev1.Pod
	for _, pod := range pods {
		// an unscheduled pod holds no capacity anywhere, and evicting it deletes it without consulting any disruption budget
		if pod.Spec.NodeName == "" || burstNodes[pod.Spec.NodeName] {
			continue
		}
		stranded = append(stranded, pod)
	}
	return evictPods(ctx, kc, stranded, opMigrate, retry)
}

func evictPods(ctx context.Context, kc kubernetes.Interface, pods []*corev1.Pod, op string, retry evictionRetry) error {
	refused, refusal, failure := evictOnce(ctx, kc, pods, op)
	for failure == nil && len(refused) > 0 && retry.allows() {
		select {
		case <-ctx.Done():
			return errors.Join(refusal, ctx.Err())
		case <-time.After(retry.delay):
		}
		refused, refusal, failure = evictOnce(ctx, kc, refused, op)
	}
	if failure != nil {
		return failure
	}
	return refusal
}

func evictOnce(ctx context.Context, kc kubernetes.Interface, pods []*corev1.Pod, op string) (refused []*corev1.Pod, refusal, failure error) {
	for _, pod := range pods {
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev)
		if err == nil {
			continue
		}
		// only a disruption budget clears on its own, so every other failure is reported at once rather than waited out
		if apierrors.IsTooManyRequests(err) {
			refused = append(refused, pod)
			recordFirst(&refusal, fmt.Errorf("%s: evict %s/%s: %w", op, pod.Namespace, pod.Name, err))
			continue
		}
		recordFirst(&failure, fmt.Errorf("%s: evict %s/%s: %w", op, pod.Namespace, pod.Name, err))
	}
	return refused, refusal, failure
}

func buildRestorePatch(kind, name, placement string, current map[string]string) ([]byte, error) {
	var saved savedPlacement
	if err := json.Unmarshal([]byte(placement), &saved); err != nil {
		return nil, fmt.Errorf("restore-placement: unmarshal placement for %s %q: %w", kind, name, err)
	}
	affinity, err := replacingAffinity(saved.Affinity, kind, name)
	if err != nil {
		return nil, err
	}
	original := podPlacementPatch{Affinity: affinity, Tolerations: saved.Tolerations}
	if len(saved.NodeSelector) > 0 {
		original.NodeSelector = nodeSelectorPatch(saved.NodeSelector, current)
	}
	patchData, err := buildPlacementPatch(clearedPlacementMarkers(), original)
	if err != nil {
		return nil, fmt.Errorf("restore-placement: marshal patch for %s %q: %w", kind, name, err)
	}
	return patchData, nil
}

func recordFirst(firstErr *error, err error) {
	if err != nil && *firstErr == nil {
		*firstErr = err
	}
}

func RestorePlacement(ctx context.Context, kc kubernetes.Interface, targets TargetSet, lease LeaseIdentity) ([]string, error) {
	if err := lease.validate(); err != nil {
		return nil, fmt.Errorf("restore-placement: %w", err)
	}

	var restored []string
	var failures error
	for _, namespace := range targets.namespaces {
		names, err := restoreNamespace(ctx, kc, namespace, lease)
		restored = append(restored, names...)
		failures = errors.Join(failures, err)
	}
	return restored, failures
}

func restoreNamespace(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) ([]string, error) {
	var restored []string
	var notSelfRolling []labels.Selector
	var firstErr error
	// restore reads every workload rather than the target selector's, so a workload whose labels changed while it was on burst is still put back
	for _, wc := range workloadClients(kc, namespace, labels.Everything()) {
		names, selectors, err := restoreWorkloads(ctx, wc, lease)
		restored = append(restored, names...)
		notSelfRolling = append(notSelfRolling, selectors...)
		recordFirst(&firstErr, err)
	}
	if len(restored) == 0 {
		return nil, firstErr
	}
	recordFirst(&firstErr, evictWorkloadPods(ctx, kc, namespace, notSelfRolling, opRestore))
	return restored, firstErr
}

func restoreWorkloads(ctx context.Context, wc workloadClient, lease LeaseIdentity) ([]string, []labels.Selector, error) {
	targets, err := wc.list(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("restore-placement: list %s in %q: %w", wc.plural(), wc.namespace, err)
	}
	var restored []string
	var notSelfRolling []labels.Selector
	var firstErr error
	for _, t := range targets {
		placement, ok := t.annotations[PrePlacementAnnotationKey]
		if !ok {
			continue
		}
		// an unowned workload is left pinned to a lease that can no longer restore it, so restoring it here is the only recovery
		if owner, owned := placementOwner(t.labels); owned && owner != lease.UID {
			continue
		}
		patchData, err := buildRestorePatch(wc.kind, t.name, placement, t.podSpec.NodeSelector)
		if err != nil {
			recordFirst(&firstErr, err)
			continue
		}
		if err := wc.patch(ctx, t.name, patchData); err != nil {
			recordFirst(&firstErr, fmt.Errorf("restore-placement: patch %s %q: %w", wc.kind, t.name, err))
			continue
		}
		restored = append(restored, wc.ref(t.name))
		if !t.selfRolls() {
			notSelfRolling = append(notSelfRolling, t.selector)
		}
	}
	return restored, notSelfRolling, firstErr
}

func requireLeaseNode(ctx context.Context, kc kubernetes.Interface, leaseUID, op string) error {
	nodes, err := leaseNodes(ctx, kc, leaseUID)
	if err != nil {
		return fmt.Errorf("%s: list nodes: %w", op, err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("%s: no node carries label %s=%s", op, LeaseUIDLabelKey, leaseUID)
	}
	return nil
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func WorkloadOnBurstNodes(ctx context.Context, kc kubernetes.Interface, targets TargetSet, lease LeaseIdentity) (bool, error) {
	const op = "workload-on-burst-nodes"
	if err := lease.validate(); err != nil {
		return false, fmt.Errorf("%s: %w", op, err)
	}

	settled := true
	placed := 0
	for _, namespace := range targets.namespaces {
		count, ok, err := namespaceOnBurstNodes(ctx, kc, namespace, lease, op)
		if err != nil {
			return false, err
		}
		settled = settled && ok
		placed += count
	}
	return settled && placed > 0, nil
}

func namespaceOnBurstNodes(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity, op string) (int, bool, error) {
	burstNodes, pods, err := leaseNodesAndPods(ctx, kc, namespace, lease, op)
	if err != nil {
		return 0, false, err
	}
	owned, err := ownedWorkloadSelectors(ctx, kc, namespace, lease)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", op, err)
	}
	placed := 0
	for i := range pods {
		p := &pods[i]
		if isDaemonSetPod(p) || isTerminalPod(p) || !matchedByWorkload(p.Labels, owned) {
			continue
		}
		if p.Status.Phase != corev1.PodRunning || !burstNodes[p.Spec.NodeName] {
			return 0, false, nil
		}
		placed++
	}
	return placed, true, nil
}

func WorkloadOffBurstNodes(ctx context.Context, kc kubernetes.Interface, targets TargetSet, lease LeaseIdentity) (bool, error) {
	const op = "workload-off-burst-nodes"
	if err := lease.validate(); err != nil {
		return false, fmt.Errorf("%s: %w", op, err)
	}

	// an empty namespace reads ready, so the fold is a conjunction rather than a shortcut that lets one empty namespace mask a stuck one
	restored := true
	for _, namespace := range targets.namespaces {
		off, err := namespaceOffBurstNodes(ctx, kc, namespace, lease, op)
		if err != nil {
			return false, err
		}
		restored = restored && off
	}
	return restored, nil
}

func namespaceOffBurstNodes(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity, op string) (bool, error) {
	// restore clears the owner marker before this gate runs, so the node a pod sits on is all that is left to say whether it still holds this lease's capacity
	burstNodes, pods, err := leaseNodesAndPods(ctx, kc, namespace, lease, op)
	if err != nil {
		return false, err
	}
	for i := range pods {
		p := &pods[i]
		if isDaemonSetPod(p) || isTerminalPod(p) {
			continue
		}
		if burstNodes[p.Spec.NodeName] {
			return false, nil
		}
	}
	return true, nil
}

func leaseNodesAndPods(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity, opName string) (map[string]bool, []corev1.Pod, error) {
	burstNodes, err := leaseNodes(ctx, kc, lease.UID)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: list nodes: %w", opName, err)
	}
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("%s: list pods in %q: %w", opName, namespace, err)
	}
	return burstNodes, pods.Items, nil
}

func ownedWorkloadSelectors(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) ([]labels.Selector, error) {
	var owned []labels.Selector
	for _, wc := range workloadClients(kc, namespace, labels.Everything()) {
		targets, err := wc.list(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s in %q: %w", wc.plural(), wc.namespace, err)
		}
		for _, t := range targets {
			if owner, ok := placementOwner(t.labels); ok && owner == lease.UID {
				owned = append(owned, t.selector)
			}
		}
	}
	return owned, nil
}

func leaseNodes(ctx context.Context, kc kubernetes.Interface, leaseUID string) (map[string]bool, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: LeaseUIDLabelKey + "=" + leaseUID,
	})
	if err != nil {
		return nil, err
	}
	burst := map[string]bool{}
	for i := range nodes.Items {
		burst[nodes.Items[i].Name] = true
	}
	return burst, nil
}

func isTerminalPod(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}
