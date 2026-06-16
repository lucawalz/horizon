package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const PoolLabelKey = "horizon.dev/pool"

type savedSpec struct {
	name             string
	originalAffinity []byte
}

type SavedState struct {
	deployments  []savedSpec
	statefulSets []savedSpec
	namespace    string
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

type affinityPatch struct {
	Spec struct {
		Template struct {
			Spec struct {
				Affinity *corev1.Affinity `json:"affinity"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func buildAffinityPatch(a *corev1.Affinity) ([]byte, error) {
	p := affinityPatch{}
	p.Spec.Template.Spec.Affinity = a
	return json.Marshal(p)
}

func Migrate(ctx context.Context, kc kubernetes.Interface, namespace, poolLabelValue string) (*SavedState, error) {
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

	affinity := &corev1.Affinity{
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

	patchData, err := buildAffinityPatch(affinity)
	if err != nil {
		return nil, fmt.Errorf("migrate: marshal affinity patch: %w", err)
	}

	deps, err := kc.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("migrate: list deployments in %q: %w", namespace, err)
	}
	state := &SavedState{namespace: namespace}
	for i := range deps.Items {
		d := deps.Items[i]
		var orig []byte
		if d.Spec.Template.Spec.Affinity != nil {
			orig, err = json.Marshal(d.Spec.Template.Spec.Affinity)
			if err != nil {
				return state, fmt.Errorf("migrate: marshal affinity for %q: %w", d.Name, err)
			}
		}
		state.deployments = append(state.deployments, savedSpec{name: d.Name, originalAffinity: orig})
		if _, err := kc.AppsV1().Deployments(namespace).Patch(ctx, d.Name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			return state, fmt.Errorf("migrate: patch deployment %q: %w", d.Name, err)
		}
	}

	stss, err := kc.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return state, fmt.Errorf("migrate: list statefulsets in %q: %w", namespace, err)
	}
	for i := range stss.Items {
		s := stss.Items[i]
		var orig []byte
		if s.Spec.Template.Spec.Affinity != nil {
			orig, err = json.Marshal(s.Spec.Template.Spec.Affinity)
			if err != nil {
				return state, fmt.Errorf("migrate: marshal affinity for %q: %w", s.Name, err)
			}
		}
		state.statefulSets = append(state.statefulSets, savedSpec{name: s.Name, originalAffinity: orig})
		if _, err := kc.AppsV1().StatefulSets(namespace).Patch(ctx, s.Name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			return state, fmt.Errorf("migrate: patch statefulset %q: %w", s.Name, err)
		}
	}

	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return state, fmt.Errorf("migrate: list pods in %q: %w", namespace, err)
	}
	for i := range pods.Items {
		pod := pods.Items[i]
		if isDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			return state, fmt.Errorf("migrate: evict %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return state, nil
}

func RollbackMigrate(ctx context.Context, kc kubernetes.Interface, state *SavedState) error {
	if state == nil {
		return nil
	}

	var firstErr error

	for _, ss := range state.deployments {
		var restored *corev1.Affinity
		if ss.originalAffinity != nil {
			restored = &corev1.Affinity{}
			if err := json.Unmarshal(ss.originalAffinity, restored); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("rollback-migrate: unmarshal affinity for deployment %q: %w", ss.name, err)
				}
				continue
			}
		}
		patchData, err := buildAffinityPatch(restored)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("rollback-migrate: marshal affinity patch for deployment %q: %w", ss.name, err)
			}
			continue
		}
		if _, err := kc.AppsV1().Deployments(state.namespace).Patch(ctx, ss.name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("rollback-migrate: patch deployment %q: %w", ss.name, err)
			}
		}
	}

	for _, ss := range state.statefulSets {
		var restored *corev1.Affinity
		if ss.originalAffinity != nil {
			restored = &corev1.Affinity{}
			if err := json.Unmarshal(ss.originalAffinity, restored); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("rollback-migrate: unmarshal affinity for statefulset %q: %w", ss.name, err)
				}
				continue
			}
		}
		patchData, err := buildAffinityPatch(restored)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("rollback-migrate: marshal affinity patch for statefulset %q: %w", ss.name, err)
			}
			continue
		}
		if _, err := kc.AppsV1().StatefulSets(state.namespace).Patch(ctx, ss.name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("rollback-migrate: patch statefulset %q: %w", ss.name, err)
			}
		}
	}

	pods, err := kc.CoreV1().Pods(state.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("rollback-migrate: list pods in %q: %w", state.namespace, err)
		}
		return firstErr
	}
	for i := range pods.Items {
		pod := pods.Items[i]
		if isDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("rollback-migrate: evict %s/%s: %w", pod.Namespace, pod.Name, err)
			}
		}
	}

	return firstErr
}

func presentPoolLabelValues(ctx context.Context, kc kubernetes.Interface) (map[string]bool, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("reconcile-affinity: list nodes: %w", err)
	}
	present := map[string]bool{}
	for i := range nodes.Items {
		n := nodes.Items[i]
		if v, ok := n.Labels[PoolLabelKey]; ok {
			present[v] = true
		}
	}
	return present, nil
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

func strippedNodeAffinity(a *corev1.Affinity, present map[string]bool) (*corev1.NodeAffinity, bool) {
	if a == nil || a.NodeAffinity == nil || a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return nil, false
	}
	terms := a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	kept := make([]corev1.NodeSelectorTerm, 0, len(terms))
	changed := false
	for _, term := range terms {
		if termStranded(term, present) {
			changed = true
			continue
		}
		kept = append(kept, term)
	}
	if !changed {
		return nil, false
	}
	na := a.NodeAffinity.DeepCopy()
	if len(kept) == 0 {
		na.RequiredDuringSchedulingIgnoredDuringExecution = nil
	} else {
		na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = kept
	}
	if nodeAffinityEmpty(na) {
		return nil, true
	}
	return na, true
}

func buildStrandedAffinityPatch(na *corev1.NodeAffinity) ([]byte, error) {
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"affinity": map[string]any{"nodeAffinity": na},
				},
			},
		},
	}
	return json.Marshal(patch)
}

func termStranded(term corev1.NodeSelectorTerm, present map[string]bool) bool {
	for _, req := range term.MatchExpressions {
		if req.Key != PoolLabelKey {
			continue
		}
		for _, v := range req.Values {
			if !present[v] {
				return true
			}
		}
	}
	return false
}

func nodeAffinityEmpty(na *corev1.NodeAffinity) bool {
	return na.RequiredDuringSchedulingIgnoredDuringExecution == nil &&
		len(na.PreferredDuringSchedulingIgnoredDuringExecution) == 0
}

func ReconcileStrandedAffinity(ctx context.Context, kc kubernetes.Interface, namespace string) error {
	if err := ValidateNamespace(namespace); err != nil {
		return fmt.Errorf("reconcile-affinity: %w", err)
	}
	present, err := presentPoolLabelValues(ctx, kc)
	if err != nil {
		return err
	}

	var firstErr error
	deps, err := kc.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("reconcile-affinity: list deployments in %q: %w", namespace, err)
	}
	for i := range deps.Items {
		d := deps.Items[i]
		na, changed := strippedNodeAffinity(d.Spec.Template.Spec.Affinity, present)
		if !changed {
			continue
		}
		patchData, err := buildStrandedAffinityPatch(na)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reconcile-affinity: marshal deployment %q: %w", d.Name, err)
			}
			continue
		}
		if _, err := kc.AppsV1().Deployments(namespace).Patch(ctx, d.Name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reconcile-affinity: patch deployment %q: %w", d.Name, err)
			}
		}
	}

	stss, err := kc.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("reconcile-affinity: list statefulsets in %q: %w", namespace, err)
		}
		return firstErr
	}
	for i := range stss.Items {
		s := stss.Items[i]
		na, changed := strippedNodeAffinity(s.Spec.Template.Spec.Affinity, present)
		if !changed {
			continue
		}
		patchData, err := buildStrandedAffinityPatch(na)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reconcile-affinity: marshal statefulset %q: %w", s.Name, err)
			}
			continue
		}
		if _, err := kc.AppsV1().StatefulSets(namespace).Patch(ctx, s.Name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reconcile-affinity: patch statefulset %q: %w", s.Name, err)
			}
		}
	}

	return firstErr
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func WaitWorkloadOnBurstNodes(ctx context.Context, kc kubernetes.Interface, namespace string, poll, timeout time.Duration) error {
	if namespace == "" {
		return fmt.Errorf("wait-pods: namespace must not be empty")
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if burstNodes, err := poolNodes(pollCtx, kc); err == nil {
			if ready, perr := workloadSpreadReady(pollCtx, kc, namespace, burstNodes); perr == nil && ready {
				return nil
			}
		}
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("wait-pods: %w", ctx.Err())
			}
			return fmt.Errorf("wait-pods: timeout after %s", timeout)
		case <-ticker.C:
		}
	}
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
