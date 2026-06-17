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

	patchData, err := buildAffinityPatch(poolNodeAffinity(poolLabelValue))
	if err != nil {
		return nil, fmt.Errorf("migrate: marshal affinity patch: %w", err)
	}

	state := &SavedState{namespace: namespace}
	if err := migrateDeployments(ctx, kc, namespace, patchData, state); err != nil {
		return state, err
	}
	if err := migrateStatefulSets(ctx, kc, namespace, patchData, state); err != nil {
		return state, err
	}
	if err := evictNonDaemonSetPods(ctx, kc, namespace, "migrate"); err != nil {
		return state, err
	}
	return state, nil
}

func migrateDeployments(ctx context.Context, kc kubernetes.Interface, namespace string, patchData []byte, state *SavedState) error {
	deps, err := kc.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("migrate: list deployments in %q: %w", namespace, err)
	}
	for i := range deps.Items {
		d := deps.Items[i]
		orig, err := marshalAffinity(d.Spec.Template.Spec.Affinity, d.Name)
		if err != nil {
			return err
		}
		state.deployments = append(state.deployments, savedSpec{name: d.Name, originalAffinity: orig})
		if _, err := kc.AppsV1().Deployments(namespace).Patch(ctx, d.Name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("migrate: patch deployment %q: %w", d.Name, err)
		}
	}
	return nil
}

func migrateStatefulSets(ctx context.Context, kc kubernetes.Interface, namespace string, patchData []byte, state *SavedState) error {
	stss, err := kc.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("migrate: list statefulsets in %q: %w", namespace, err)
	}
	for i := range stss.Items {
		s := stss.Items[i]
		orig, err := marshalAffinity(s.Spec.Template.Spec.Affinity, s.Name)
		if err != nil {
			return err
		}
		state.statefulSets = append(state.statefulSets, savedSpec{name: s.Name, originalAffinity: orig})
		if _, err := kc.AppsV1().StatefulSets(namespace).Patch(ctx, s.Name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("migrate: patch statefulset %q: %w", s.Name, err)
		}
	}
	return nil
}

func marshalAffinity(a *corev1.Affinity, name string) ([]byte, error) {
	if a == nil {
		return nil, nil
	}
	orig, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("migrate: marshal affinity for %q: %w", name, err)
	}
	return orig, nil
}

func evictNonDaemonSetPods(ctx context.Context, kc kubernetes.Interface, namespace, op string) error {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("%s: list pods in %q: %w", op, namespace, err)
	}
	for i := range pods.Items {
		pod := pods.Items[i]
		if isDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			return fmt.Errorf("%s: evict %s/%s: %w", op, pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func restoreAffinityPatch(ss savedSpec, kind string) ([]byte, error) {
	var restored *corev1.Affinity
	if ss.originalAffinity != nil {
		restored = &corev1.Affinity{}
		if err := json.Unmarshal(ss.originalAffinity, restored); err != nil {
			return nil, fmt.Errorf("rollback-migrate: unmarshal affinity for %s %q: %w", kind, ss.name, err)
		}
	}
	patchData, err := buildAffinityPatch(restored)
	if err != nil {
		return nil, fmt.Errorf("rollback-migrate: marshal affinity patch for %s %q: %w", kind, ss.name, err)
	}
	return patchData, nil
}

func recordFirst(firstErr *error, err error) {
	if err != nil && *firstErr == nil {
		*firstErr = err
	}
}

func RollbackMigrate(ctx context.Context, kc kubernetes.Interface, state *SavedState) error {
	if state == nil {
		return nil
	}

	var firstErr error

	for _, ss := range state.deployments {
		patchData, err := restoreAffinityPatch(ss, "deployment")
		if err != nil {
			recordFirst(&firstErr, err)
			continue
		}
		if _, err := kc.AppsV1().Deployments(state.namespace).Patch(ctx, ss.name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			recordFirst(&firstErr, fmt.Errorf("rollback-migrate: patch deployment %q: %w", ss.name, err))
		}
	}

	for _, ss := range state.statefulSets {
		patchData, err := restoreAffinityPatch(ss, "statefulset")
		if err != nil {
			recordFirst(&firstErr, err)
			continue
		}
		if _, err := kc.AppsV1().StatefulSets(state.namespace).Patch(ctx, ss.name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			recordFirst(&firstErr, fmt.Errorf("rollback-migrate: patch statefulset %q: %w", ss.name, err))
		}
	}

	recordFirst(&firstErr, evictNonDaemonSetPodsBestEffort(ctx, kc, state.namespace))
	return firstErr
}

func evictNonDaemonSetPodsBestEffort(ctx context.Context, kc kubernetes.Interface, namespace string) error {
	pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("rollback-migrate: list pods in %q: %w", namespace, err)
	}
	var firstErr error
	for i := range pods.Items {
		pod := pods.Items[i]
		if isDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			recordFirst(&firstErr, fmt.Errorf("rollback-migrate: evict %s/%s: %w", pod.Namespace, pod.Name, err))
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
