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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	PoolLabelKey              = provider.PoolLabelKey
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
	Affinity    *corev1.Affinity    `json:"affinity,omitempty"`
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

type metadataPatch struct {
	Annotations map[string]*string `json:"annotations"`
	Labels      map[string]*string `json:"labels"`
}

type placementPatch struct {
	Metadata metadataPatch `json:"metadata"`
	Spec     struct {
		Template struct {
			Spec struct {
				Affinity    any                 `json:"affinity"`
				Tolerations []corev1.Toleration `json:"tolerations"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func buildPlacementPatch(meta metadataPatch, affinity any, tolerations []corev1.Toleration) ([]byte, error) {
	p := placementPatch{Metadata: meta}
	p.Spec.Template.Spec.Affinity = affinity
	p.Spec.Template.Spec.Tolerations = tolerations
	return json.Marshal(p)
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

func savedPlacementMarkers(placement string) metadataPatch {
	value := BurstPlacementLabelValue
	return metadataPatch{
		Annotations: map[string]*string{PrePlacementAnnotationKey: &placement},
		Labels:      map[string]*string{BurstPlacementLabelKey: &value},
	}
}

func clearedPlacementMarkers() metadataPatch {
	return metadataPatch{
		Annotations: map[string]*string{PrePlacementAnnotationKey: nil},
		Labels:      map[string]*string{BurstPlacementLabelKey: nil},
	}
}

func burstToleration() corev1.Toleration {
	return corev1.Toleration{
		Key:      BurstTaintKey,
		Operator: corev1.TolerationOpExists,
		Effect:   burstTaintEffect,
	}
}

func poolNodeAffinity(poolLabelValue string) *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      PoolLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{poolLabelValue},
					}},
				}},
			},
		},
	}
}

type workloadTarget struct {
	name        string
	annotations map[string]string
	podSpec     *corev1.PodSpec
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
				targets = append(targets, workloadTarget{
					name:        item.Name,
					annotations: item.Annotations,
					podSpec:     &item.Spec.Template.Spec,
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
				targets = append(targets, workloadTarget{
					name:        item.Name,
					annotations: item.Annotations,
					podSpec:     &item.Spec.Template.Spec,
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

func Migrate(ctx context.Context, kc kubernetes.Interface, namespace, poolLabelValue string) ([]string, error) {
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if poolLabelValue == "" {
		return nil, fmt.Errorf("migrate: pool label value must not be empty")
	}

	hasNode, err := poolNodePresent(ctx, kc, poolLabelValue)
	if err != nil {
		return nil, err
	}
	if !hasNode {
		return nil, fmt.Errorf("migrate: no node carries label %s=%s", PoolLabelKey, poolLabelValue)
	}

	targetAffinity := poolNodeAffinity(poolLabelValue)

	var onBurst []string
	moved := false
	for _, wc := range workloadClients(kc, namespace) {
		names, patched, err := migrateWorkloads(ctx, wc, targetAffinity)
		onBurst = append(onBurst, names...)
		moved = moved || patched
		if err != nil {
			return onBurst, err
		}
	}
	if !moved {
		return onBurst, nil
	}
	if err := evictNonDaemonSetPods(ctx, kc, namespace); err != nil {
		return onBurst, err
	}
	return onBurst, nil
}

func migrateWorkloads(ctx context.Context, wc workloadClient, affinity *corev1.Affinity) (onBurst []string, patched bool, err error) {
	targets, err := wc.list(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("migrate: list %s in %q: %w", wc.plural(), wc.namespace, err)
	}
	for _, t := range targets {
		if _, ok := t.annotations[PrePlacementAnnotationKey]; !ok {
			patchData, err := buildMigratePatch(t, affinity)
			if err != nil {
				return onBurst, patched, err
			}
			if err := wc.patch(ctx, t.name, patchData); err != nil {
				return onBurst, patched, fmt.Errorf("migrate: patch %s %q: %w", wc.kind, t.name, err)
			}
			patched = true
		}
		onBurst = append(onBurst, wc.kind+"/"+t.name)
	}
	return onBurst, patched, nil
}

func buildMigratePatch(t workloadTarget, affinity *corev1.Affinity) ([]byte, error) {
	placement, err := marshalPlacement(t.podSpec, t.name)
	if err != nil {
		return nil, err
	}
	patchData, err := buildPlacementPatch(savedPlacementMarkers(placement), affinity, withBurstToleration(t.podSpec.Tolerations))
	if err != nil {
		return nil, fmt.Errorf("migrate: marshal patch for %q: %w", t.name, err)
	}
	return patchData, nil
}

func withBurstToleration(existing []corev1.Toleration) []corev1.Toleration {
	for _, t := range existing {
		if t.Key == BurstTaintKey && t.Effect == burstTaintEffect && t.Operator == corev1.TolerationOpExists {
			return existing
		}
	}
	return append(append([]corev1.Toleration{}, existing...), burstToleration())
}

func marshalPlacement(podSpec *corev1.PodSpec, name string) (string, error) {
	data, err := json.Marshal(savedPlacement{Affinity: podSpec.Affinity, Tolerations: podSpec.Tolerations})
	if err != nil {
		return "", fmt.Errorf("migrate: marshal placement for %q: %w", name, err)
	}
	return string(data), nil
}

func evictNonDaemonSetPods(ctx context.Context, kc kubernetes.Interface, namespace string) error {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("migrate: list pods in %q: %w", namespace, err)
	}
	for i := range pods.Items {
		pod := pods.Items[i]
		if isDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			return fmt.Errorf("migrate: evict %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func buildRestorePatch(kind, name, placement string) ([]byte, error) {
	var saved savedPlacement
	if err := json.Unmarshal([]byte(placement), &saved); err != nil {
		return nil, fmt.Errorf("restore-placement: unmarshal placement for %s %q: %w", kind, name, err)
	}
	affinity, err := replacingAffinity(saved.Affinity, kind, name)
	if err != nil {
		return nil, err
	}
	patchData, err := buildPlacementPatch(clearedPlacementMarkers(), affinity, saved.Tolerations)
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

func RestorePlacement(ctx context.Context, kc kubernetes.Interface, namespace string) ([]string, error) {
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("restore-placement: %w", err)
	}

	var restored []string
	var firstErr error
	for _, wc := range workloadClients(kc, namespace) {
		names, err := restoreWorkloads(ctx, wc)
		restored = append(restored, names...)
		recordFirst(&firstErr, err)
	}
	if len(restored) == 0 {
		return nil, firstErr
	}
	recordFirst(&firstErr, evictNonDaemonSetPodsBestEffort(ctx, kc, namespace))
	return restored, firstErr
}

func restoreWorkloads(ctx context.Context, wc workloadClient) ([]string, error) {
	targets, err := wc.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("restore-placement: list %s in %q: %w", wc.plural(), wc.namespace, err)
	}
	var restored []string
	var firstErr error
	for _, t := range targets {
		placement, ok := t.annotations[PrePlacementAnnotationKey]
		if !ok {
			continue
		}
		patchData, err := buildRestorePatch(wc.kind, t.name, placement)
		if err != nil {
			recordFirst(&firstErr, err)
			continue
		}
		if err := wc.patch(ctx, t.name, patchData); err != nil {
			recordFirst(&firstErr, fmt.Errorf("restore-placement: patch %s %q: %w", wc.kind, t.name, err))
			continue
		}
		restored = append(restored, wc.kind+"/"+t.name)
	}
	return restored, firstErr
}

func evictNonDaemonSetPodsBestEffort(ctx context.Context, kc kubernetes.Interface, namespace string) error {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("restore-placement: list pods in %q: %w", namespace, err)
	}
	var firstErr error
	for i := range pods.Items {
		pod := pods.Items[i]
		if isDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			recordFirst(&firstErr, fmt.Errorf("restore-placement: evict %s/%s: %w", pod.Namespace, pod.Name, err))
		}
	}
	return firstErr
}

func poolNodePresent(ctx context.Context, kc kubernetes.Interface, poolLabelValue string) (bool, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("migrate: list nodes: %w", err)
	}
	for i := range nodes.Items {
		if nodes.Items[i].Labels[PoolLabelKey] == poolLabelValue {
			return true, nil
		}
	}
	return false, nil
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func WorkloadOnBurstNodes(ctx context.Context, kc kubernetes.Interface, namespace string) (bool, error) {
	if namespace == "" {
		return false, fmt.Errorf("workload-on-burst-nodes: namespace must not be empty")
	}
	burstNodes, err := poolNodes(ctx, kc)
	if err != nil {
		return false, fmt.Errorf("workload-on-burst-nodes: list nodes: %w", err)
	}
	spread, err := workloadSpreadReady(ctx, kc, namespace, burstNodes)
	if err != nil {
		return false, fmt.Errorf("workload-on-burst-nodes: list pods in %q: %w", namespace, err)
	}
	return spread, nil
}

func poolNodes(ctx context.Context, kc kubernetes.Interface) (map[string]bool, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	burst := map[string]bool{}
	for i := range nodes.Items {
		if _, ok := nodes.Items[i].Labels[PoolLabelKey]; ok {
			burst[nodes.Items[i].Name] = true
		}
	}
	return burst, nil
}

func workloadSpreadReady(ctx context.Context, kc kubernetes.Interface, namespace string, burstNodes map[string]bool) (bool, error) {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	counted := 0
	for i := range pods.Items {
		p := pods.Items[i]
		if isDaemonSetPod(&p) {
			continue
		}
		counted++
		if p.Status.Phase != corev1.PodRunning || !burstNodes[p.Spec.NodeName] {
			return false, nil
		}
	}
	return counted > 0, nil
}
