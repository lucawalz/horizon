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

const NodeAffinityLabelKey = "horizon.dev/burst-workload"

type savedSpec struct {
	name             string
	originalAffinity []byte
}

type SavedState struct {
	deployments  []savedSpec
	statefulSets []savedSpec
	nodeName     string
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

func Migrate(ctx context.Context, kc kubernetes.Interface, namespace, nodeName string) (*SavedState, error) {
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	labelPatch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{NodeAffinityLabelKey: namespace},
		},
	}
	data, err := json.Marshal(labelPatch)
	if err != nil {
		return nil, fmt.Errorf("migrate: marshal label patch: %w", err)
	}
	if _, err := kc.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, data, metav1.PatchOptions{}); err != nil {
		return nil, fmt.Errorf("migrate: label node %q: %w", nodeName, err)
	}

	affinity := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      NodeAffinityLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{namespace},
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
	state := &SavedState{nodeName: nodeName, namespace: namespace}
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

	removePatch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{NodeAffinityLabelKey: nil},
		},
	}
	removeData, err := json.Marshal(removePatch)
	if err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("rollback-migrate: marshal remove label patch: %w", err)
		}
	} else if _, err := kc.CoreV1().Nodes().Patch(ctx, state.nodeName, types.MergePatchType, removeData, metav1.PatchOptions{}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("rollback-migrate: remove node label: %w", err)
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

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func IsDaemonSetPod(pod *corev1.Pod) bool { return isDaemonSetPod(pod) }

func WaitPodsRunningOnNode(ctx context.Context, kc kubernetes.Interface, namespace, nodeName string, poll, timeout time.Duration) error {
	if namespace == "" {
		return fmt.Errorf("wait-pods: namespace must not be empty")
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		pods, err := kc.CoreV1().Pods(namespace).List(pollCtx, metav1.ListOptions{})
		if err == nil {
			allReady := false
			counted := 0
			for _, p := range pods.Items {
				if isDaemonSetPod(&p) {
					continue
				}
				counted++
				if p.Spec.NodeName != nodeName || p.Status.Phase != corev1.PodRunning {
					counted = -1
					break
				}
			}
			if counted > 0 {
				allReady = true
			}
			if allReady {
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
