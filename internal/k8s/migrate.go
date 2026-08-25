package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/lucawalz/horizon/internal/provider"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
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
)

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
	Annotations map[string]*string `json:"annotations"`
	Labels      map[string]*string `json:"labels"`
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

func workloadRef(kind, name string) string {
	return kind + "/" + name
}

func workloadSelector(kind, name string, sel *metav1.LabelSelector) (labels.Selector, error) {
	if sel != nil && len(sel.MatchLabels)+len(sel.MatchExpressions) == 0 {
		return nil, fmt.Errorf("selector for %s %q: empty selector would match every pod in the namespace", kind, name)
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

func deploymentClient(kc kubernetes.Interface, namespace string) workloadClient {
	api := kc.AppsV1().Deployments(namespace)
	return workloadClient{
		kind:      kindDeployment,
		namespace: namespace,
		list: func(ctx context.Context) ([]workloadTarget, error) {
			list, err := api.List(ctx, metav1.ListOptions{})
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

func statefulSetClient(kc kubernetes.Interface, namespace string) workloadClient {
	api := kc.AppsV1().StatefulSets(namespace)
	return workloadClient{
		kind:      kindStatefulSet,
		namespace: namespace,
		list: func(ctx context.Context) ([]workloadTarget, error) {
			list, err := api.List(ctx, metav1.ListOptions{})
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

func workloadClients(kc kubernetes.Interface, namespace string) []workloadClient {
	return []workloadClient{deploymentClient(kc, namespace), statefulSetClient(kc, namespace)}
}

func Migrate(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) ([]string, error) {
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := lease.validate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	hasNode, err := leaseNodePresent(ctx, kc, lease.UID)
	if err != nil {
		return nil, err
	}
	if !hasNode {
		return nil, fmt.Errorf("migrate: no node carries label %s=%s", LeaseUIDLabelKey, lease.UID)
	}

	var onBurst []string
	var notSelfRolling []labels.Selector
	moved := false
	for _, wc := range workloadClients(kc, namespace) {
		names, patched, selectors, err := migrateWorkloads(ctx, wc, lease)
		onBurst = append(onBurst, names...)
		notSelfRolling = append(notSelfRolling, selectors...)
		moved = moved || patched
		if err != nil {
			return onBurst, err
		}
	}
	if !moved {
		return onBurst, nil
	}
	if err := evictWorkloadPods(ctx, kc, namespace, notSelfRolling); err != nil {
		return onBurst, err
	}
	return onBurst, nil
}

func migrateWorkloads(ctx context.Context, wc workloadClient, lease LeaseIdentity) (onBurst []string, patched bool, notSelfRolling []labels.Selector, err error) {
	targets, err := wc.list(ctx)
	if err != nil {
		return nil, false, nil, fmt.Errorf("migrate: list %s in %q: %w", wc.plural(), wc.namespace, err)
	}
	for _, t := range targets {
		if _, alreadyMoved := t.annotations[PrePlacementAnnotationKey]; alreadyMoved {
			// a marker written before ownership was recorded names no lease, so no other lease can be holding it
			if owner, owned := placementOwner(t.labels); !owned || owner == lease.UID {
				onBurst = append(onBurst, workloadRef(wc.kind, t.name))
			}
			continue
		}
		patchData, err := buildMigratePatch(t, lease)
		if err != nil {
			return onBurst, patched, notSelfRolling, err
		}
		if err := wc.patch(ctx, t.name, patchData); err != nil {
			return onBurst, patched, notSelfRolling, fmt.Errorf("migrate: patch %s %q: %w", wc.kind, t.name, err)
		}
		patched = true
		if !t.selfRolls() {
			notSelfRolling = append(notSelfRolling, t.selector)
		}
		onBurst = append(onBurst, workloadRef(wc.kind, t.name))
	}
	return onBurst, patched, notSelfRolling, nil
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

func evictablePods(pods []corev1.Pod, workloads []labels.Selector) []*corev1.Pod {
	var evictable []*corev1.Pod
	for i := range pods {
		pod := &pods[i]
		if isDaemonSetPod(pod) {
			continue
		}
		if !matchedByWorkload(pod.Labels, workloads) {
			continue
		}
		evictable = append(evictable, pod)
	}
	return evictable
}

func matchedByWorkload(podLabels map[string]string, workloads []labels.Selector) bool {
	set := labels.Set(podLabels)
	for _, sel := range workloads {
		if sel.Matches(set) {
			return true
		}
	}
	return false
}

func evictWorkloadPods(ctx context.Context, kc kubernetes.Interface, namespace string, workloads []labels.Selector) error {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("migrate: list pods in %q: %w", namespace, err)
	}
	for _, pod := range evictablePods(pods.Items, workloads) {
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			return fmt.Errorf("migrate: evict %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
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

func RestorePlacement(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) ([]string, error) {
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("restore-placement: %w", err)
	}
	if err := lease.validate(); err != nil {
		return nil, fmt.Errorf("restore-placement: %w", err)
	}

	var restored []string
	var notSelfRolling []labels.Selector
	var firstErr error
	for _, wc := range workloadClients(kc, namespace) {
		names, selectors, err := restoreWorkloads(ctx, wc, lease)
		restored = append(restored, names...)
		notSelfRolling = append(notSelfRolling, selectors...)
		recordFirst(&firstErr, err)
	}
	if len(restored) == 0 {
		return nil, firstErr
	}
	recordFirst(&firstErr, evictWorkloadPodsBestEffort(ctx, kc, namespace, notSelfRolling))
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
		restored = append(restored, workloadRef(wc.kind, t.name))
		if !t.selfRolls() {
			notSelfRolling = append(notSelfRolling, t.selector)
		}
	}
	return restored, notSelfRolling, firstErr
}

func evictWorkloadPodsBestEffort(ctx context.Context, kc kubernetes.Interface, namespace string, workloads []labels.Selector) error {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("restore-placement: list pods in %q: %w", namespace, err)
	}
	var firstErr error
	for _, pod := range evictablePods(pods.Items, workloads) {
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			recordFirst(&firstErr, fmt.Errorf("restore-placement: evict %s/%s: %w", pod.Namespace, pod.Name, err))
		}
	}
	return firstErr
}

func leaseNodePresent(ctx context.Context, kc kubernetes.Interface, leaseUID string) (bool, error) {
	nodes, err := leaseNodes(ctx, kc, leaseUID)
	if err != nil {
		return false, fmt.Errorf("migrate: list nodes: %w", err)
	}
	return len(nodes) > 0, nil
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func WorkloadOnBurstNodes(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) (bool, error) {
	return workloadNodeMembership(ctx, kc, namespace, lease, true, false, "workload-on-burst-nodes")
}

func WorkloadOffBurstNodes(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity) (bool, error) {
	// an empty namespace has nothing left on a burst node to drain, and restore already patched placement back so nothing new can land there
	return workloadNodeMembership(ctx, kc, namespace, lease, false, true, "workload-off-burst-nodes")
}

func workloadNodeMembership(ctx context.Context, kc kubernetes.Interface, namespace string, lease LeaseIdentity, wantBurst, emptyIsReady bool, opName string) (bool, error) {
	if namespace == "" {
		return false, fmt.Errorf("%s: namespace must not be empty", opName)
	}
	if err := lease.validate(); err != nil {
		return false, fmt.Errorf("%s: %w", opName, err)
	}
	burstNodes, err := leaseNodes(ctx, kc, lease.UID)
	if err != nil {
		return false, fmt.Errorf("%s: list nodes: %w", opName, err)
	}
	spread, err := workloadSpreadReady(ctx, kc, namespace, burstNodes, wantBurst, emptyIsReady)
	if err != nil {
		return false, fmt.Errorf("%s: list pods in %q: %w", opName, namespace, err)
	}
	return spread, nil
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

func workloadSpreadReady(ctx context.Context, kc kubernetes.Interface, namespace string, burstNodes map[string]bool, wantBurst, emptyIsReady bool) (bool, error) {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	counted := 0
	for i := range pods.Items {
		p := pods.Items[i]
		if isDaemonSetPod(&p) || isTerminalPod(&p) {
			continue
		}
		counted++
		if p.Status.Phase != corev1.PodRunning || burstNodes[p.Spec.NodeName] != wantBurst {
			return false, nil
		}
	}
	if counted == 0 {
		return emptyIsReady, nil
	}
	return true, nil
}
