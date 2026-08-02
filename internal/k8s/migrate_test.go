package k8s_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func makePod(name, ns, node string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func makeDSPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "DaemonSet",
				Name:       "ds",
				APIVersion: "apps/v1",
				UID:        "u",
				Controller: boolPtr(true),
			}},
		},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

const (
	poolValue = "burst"
	testNS    = "sentio-systems"
)

func patchBodies(kc *fake.Clientset, resource, name string) []string {
	var bodies []string
	for _, a := range kc.Actions() {
		if a.GetVerb() != "patch" || a.GetResource().Resource != resource {
			continue
		}
		pa, ok := a.(k8stesting.PatchAction)
		if !ok || (name != "" && pa.GetName() != name) {
			continue
		}
		bodies = append(bodies, string(pa.GetPatch()))
	}
	return bodies
}

func patchTypes(kc *fake.Clientset, resource string) []types.PatchType {
	var kinds []types.PatchType
	for _, a := range kc.Actions() {
		if a.GetVerb() != "patch" || a.GetResource().Resource != resource {
			continue
		}
		if pa, ok := a.(k8stesting.PatchAction); ok {
			kinds = append(kinds, pa.GetPatchType())
		}
	}
	return kinds
}

func getDeployment(t *testing.T, kc *fake.Clientset, name string) *appsv1.Deployment {
	t.Helper()
	dep, err := kc.AppsV1().Deployments(testNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment %q: %v", name, err)
	}
	return dep
}

func tolerationKeyJSON(key string) string {
	return `"key":"` + key + `"`
}

func burstSelector() string {
	return k8s.BurstPlacementLabelKey + "=" + k8s.BurstPlacementLabelValue
}

func selectedNames(t *testing.T, kc *fake.Clientset) []string {
	t.Helper()
	ctx := context.Background()
	opts := metav1.ListOptions{LabelSelector: burstSelector()}
	var names []string
	deps, err := kc.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, opts)
	if err != nil {
		t.Fatalf("list deployments by burst label: %v", err)
	}
	for i := range deps.Items {
		names = append(names, "deployment/"+deps.Items[i].Name)
	}
	stss, err := kc.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, opts)
	if err != nil {
		t.Fatalf("list statefulsets by burst label: %v", err)
	}
	for i := range stss.Items {
		names = append(names, "statefulset/"+stss.Items[i].Name)
	}
	return names
}

func TestMigrateEviction(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS}}
	appPod := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	dsPod := makeDSPod("ds-pod", testNS, "homelab-1")
	otherPod := makePod("other-pod", "default", "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, appPod, dsPod, otherPod)
	evictAndDelete(kc)

	migrated, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 1 || migrated[0] != "deployment/app" {
		t.Errorf("migrated = %v, want [deployment/app]", migrated)
	}

	var evictions []k8stesting.CreateAction
	for _, a := range kc.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			evictions = append(evictions, a.(k8stesting.CreateAction))
		}
	}
	if len(evictions) != 1 {
		t.Fatalf("eviction count = %d, want 1", len(evictions))
	}
	ev := evictions[0].GetObject().(interface{ GetName() string })
	if ev.GetName() != "app-pod" {
		t.Errorf("evicted pod = %q, want app-pod", ev.GetName())
	}
}

func TestMigrateDoesNotRelabelNode(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	kc := fake.NewSimpleClientset(node)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" && a.GetResource().Resource == "nodes" {
			t.Errorf("Migrate must not patch nodes, got %v", a)
		}
	}
}

func TestMigrateFailsWhenNoPoolNode(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "homelab-1"}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS}}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	_, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err == nil {
		t.Fatal("expected error when no node carries the pool label")
	}
	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" {
			t.Errorf("Migrate must not mutate workloads before the pool-node check: %v", a)
		}
	}
}

func TestMigrateRejectsInvalidInput(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.Migrate(context.Background(), kc, "Foo", poolValue); err == nil {
		t.Error("expected error for an invalid namespace")
	}
	if _, err := k8s.Migrate(context.Background(), kc, testNS, ""); err == nil {
		t.Error("expected error for an empty pool label value")
	}
}

func TestMigrateAffinityPatch(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS}}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS}}
	kc := fake.NewSimpleClientset(node, dep, sts)
	evictAndDelete(kc)

	migrated, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 2 || migrated[0] != "deployment/dep1" || migrated[1] != "statefulset/sts1" {
		t.Errorf("migrated = %v, want [deployment/dep1 statefulset/sts1]", migrated)
	}

	for _, resource := range []string{"deployments", "statefulsets"} {
		kinds := patchTypes(kc, resource)
		if len(kinds) != 1 {
			t.Errorf("%s patch count = %d, want 1", resource, len(kinds))
			continue
		}
		if kinds[0] != types.StrategicMergePatchType {
			t.Errorf("%s patch type = %v, want StrategicMergePatchType", resource, kinds[0])
		}
		body := patchBodies(kc, resource, "")[0]
		for _, want := range []string{k8s.PoolLabelKey, poolValue, k8s.BurstTaintKey} {
			if !strings.Contains(body, want) {
				t.Errorf("%s patch missing %q: %s", resource, want, body)
			}
		}
	}
}

func TestMigrateRecordsPlacementInTheSamePatch(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS}}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	bodies := patchBodies(kc, "deployments", "dep1")
	if len(bodies) != 1 {
		t.Fatalf("dep1 patch count = %d, want 1: the record must ride the affinity rewrite", len(bodies))
	}
	if !strings.Contains(bodies[0], k8s.PrePlacementAnnotationKey) {
		t.Errorf("patch missing the placement annotation: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], k8s.BurstPlacementLabelKey) {
		t.Errorf("patch missing the placement label: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], "nodeAffinity") {
		t.Errorf("patch missing the affinity rewrite: %s", bodies[0])
	}
}

func TestMigrateSavedPlacementHoldsOriginalAffinityAndTolerations(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	originalAffinity := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight:          100,
				PodAffinityTerm: corev1.PodAffinityTerm{TopologyKey: "kubernetes.io/hostname"},
			}},
		},
	}
	originalToleration := corev1.Toleration{Key: "workload", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: originalAffinity, Tolerations: []corev1.Toleration{originalToleration}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	stored := getDeployment(t, kc, "dep1")
	if stored.Labels[k8s.BurstPlacementLabelKey] != k8s.BurstPlacementLabelValue {
		t.Errorf("burst placement label = %q, want %q", stored.Labels[k8s.BurstPlacementLabelKey], k8s.BurstPlacementLabelValue)
	}
	raw, ok := stored.Annotations[k8s.PrePlacementAnnotationKey]
	if !ok {
		t.Fatal("migrated deployment carries no placement annotation")
	}

	var saved struct {
		Affinity    *corev1.Affinity    `json:"affinity"`
		Tolerations []corev1.Toleration `json:"tolerations"`
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatalf("unmarshal placement annotation: %v", err)
	}
	if saved.Affinity == nil || saved.Affinity.PodAntiAffinity == nil {
		t.Fatalf("saved affinity = %+v, want the original podAntiAffinity", saved.Affinity)
	}
	if saved.Affinity.NodeAffinity != nil {
		t.Error("saved affinity must predate the burst nodeAffinity")
	}
	if len(saved.Tolerations) != 1 || saved.Tolerations[0].Key != "workload" {
		t.Errorf("saved tolerations = %+v, want the original toleration only", saved.Tolerations)
	}
}

func TestMigrateSkipsAlreadyMigratedWorkload(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: `{}`},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	migrated, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 0 {
		t.Errorf("migrated = %v, want none: the placement record is already written", migrated)
	}
	if bodies := patchBodies(kc, "deployments", "dep1"); len(bodies) != 0 {
		t.Errorf("dep1 patched again, which would overwrite the saved placement: %v", bodies)
	}
	if got := getDeployment(t, kc, "dep1").Annotations[k8s.PrePlacementAnnotationKey]; got != `{}` {
		t.Errorf("placement annotation = %q, want it left untouched", got)
	}
}

func TestMigrateSavesOriginalAffinity(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}

	originalAffinity := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: "kubernetes.io/hostname",
				},
			}},
		},
	}

	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: originalAffinity},
			},
		},
	}
	dep2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep2", Namespace: testNS},
	}

	kc := fake.NewSimpleClientset(node, dep1, dep2)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	kc.ClearActions()

	if _, err := k8s.RestorePlacement(context.Background(), kc, testNS); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	dep1Body := patchBodies(kc, "deployments", "dep1")
	if len(dep1Body) != 1 {
		t.Fatalf("dep1 restore patch count = %d, want 1", len(dep1Body))
	}
	if !strings.Contains(dep1Body[0], "podAntiAffinity") || strings.Contains(dep1Body[0], "nodeAffinity") {
		t.Errorf("dep1 restore patch missing original podAntiAffinity or still contains nodeAffinity: %s", dep1Body[0])
	}
	dep2Body := patchBodies(kc, "deployments", "dep2")
	if len(dep2Body) != 1 {
		t.Fatalf("dep2 restore patch count = %d, want 1", len(dep2Body))
	}
	if !strings.Contains(dep2Body[0], `"affinity":null`) {
		t.Errorf("dep2 restore patch does not set affinity to null: %s", dep2Body[0])
	}

	restored := getDeployment(t, kc, "dep1")
	if restored.Spec.Template.Spec.Affinity == nil || restored.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		t.Errorf("restored affinity = %+v, want the original podAntiAffinity and no burst pin", restored.Spec.Template.Spec.Affinity)
	}
	if cleared := getDeployment(t, kc, "dep2"); cleared.Spec.Template.Spec.Affinity != nil {
		t.Errorf("dep2 affinity = %+v, want nil", cleared.Spec.Template.Spec.Affinity)
	}
	if _, ok := restored.Annotations[k8s.PrePlacementAnnotationKey]; ok {
		t.Error("restore must remove the placement annotation")
	}
	if _, ok := restored.Labels[k8s.BurstPlacementLabelKey]; ok {
		t.Error("restore must remove the placement label")
	}
}

func TestRestorePlacementNeedsNoInProcessState(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	existingAffinity := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight:          50,
				PodAffinityTerm: corev1.PodAffinityTerm{TopologyKey: "kubernetes.io/hostname"},
			}},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: existingAffinity}},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	restored, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "deployment/dep1" {
		t.Fatalf("restored = %v, want [deployment/dep1]", restored)
	}

	got := getDeployment(t, kc, "dep1").Spec.Template.Spec.Affinity
	if got == nil || got.PodAntiAffinity == nil || got.NodeAffinity != nil {
		t.Errorf("affinity = %+v, want the original podAntiAffinity and no nodeAffinity", got)
	}
}

func TestBurstPlacementLabelFindsMigratedWorkloads(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS}}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS}}
	untouched := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "default"}}
	kc := fake.NewSimpleClientset(node, dep, sts, untouched)
	evictAndDelete(kc)

	if got := selectedNames(t, kc); len(got) != 0 {
		t.Fatalf("burst label query before migrate = %v, want none", got)
	}

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got := selectedNames(t, kc)
	want := []string{"deployment/dep1", "statefulset/sts1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("burst label query = %v, want %v", got, want)
	}

	if _, err := k8s.RestorePlacement(context.Background(), kc, testNS); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if got := selectedNames(t, kc); len(got) != 0 {
		t.Errorf("burst label query after restore = %v, want none", got)
	}
}

func TestRestorePlacementRestoresTolerationsAndLeavesNode(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}

	existingToleration := corev1.Toleration{Key: "workload", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{existingToleration}},
			},
		},
	}
	sts1 := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS}}

	kc := fake.NewSimpleClientset(node, dep1, sts1)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	migrateBody := patchBodies(kc, "deployments", "dep1")[0]
	if !strings.Contains(migrateBody, tolerationKeyJSON(k8s.BurstTaintKey)) {
		t.Errorf("migrate dep1 patch missing burst toleration: %s", migrateBody)
	}
	if !strings.Contains(migrateBody, "workload") {
		t.Errorf("migrate dep1 patch dropped the original toleration: %s", migrateBody)
	}
	kc.ClearActions()

	if _, err := k8s.RestorePlacement(context.Background(), kc, testNS); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" && a.GetResource().Resource == "nodes" {
			t.Errorf("restore must not patch nodes: %v", a)
		}
	}
	rollbackBody := patchBodies(kc, "deployments", "dep1")[0]
	if strings.Contains(rollbackBody, tolerationKeyJSON(k8s.BurstTaintKey)) {
		t.Errorf("restore dep1 patch must not retain burst toleration: %s", rollbackBody)
	}
	if !strings.Contains(rollbackBody, "workload") {
		t.Errorf("restore dep1 patch must restore original toleration: %s", rollbackBody)
	}

	tolerations := getDeployment(t, kc, "dep1").Spec.Template.Spec.Tolerations
	if len(tolerations) != 1 || tolerations[0].Key != "workload" {
		t.Errorf("restored tolerations = %+v, want the original toleration only", tolerations)
	}

	n, err := kc.CoreV1().Nodes().Get(context.Background(), "burst-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if n.Labels[k8s.PoolLabelKey] != poolValue {
		t.Error("node pool label must be left intact after restore")
	}
}

func TestRestorePlacementIsIdempotent(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS}}
	pod := makePod("app-pod", testNS, "burst-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(node, dep, pod)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	first, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("first RestorePlacement: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first restore = %v, want one object", first)
	}
	kc.ClearActions()

	second, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("second RestorePlacement: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second restore = %v, want none", second)
	}
	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" || a.GetSubresource() == "eviction" {
			t.Errorf("second restore must be a no-op, got %v on %v", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

func TestRestorePlacementWithoutAnnotationIsNoop(t *testing.T) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS}}
	pod := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(dep, pod)
	evictAndDelete(kc)

	restored, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("RestorePlacement = %v, want nil", err)
	}
	if len(restored) != 0 {
		t.Errorf("restored = %v, want none", restored)
	}
	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" || a.GetSubresource() == "eviction" {
			t.Errorf("restore of an unmigrated namespace must not mutate anything, got %v", a)
		}
	}
}

func TestRestorePlacementRejectsInvalidNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.RestorePlacement(context.Background(), kc, ""); err == nil {
		t.Fatal("expected error for an empty namespace")
	}
}

func TestRestorePlacementReportsUnreadableAnnotation(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: "{not json"},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
	}
	kc := fake.NewSimpleClientset(dep)
	evictAndDelete(kc)

	restored, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err == nil {
		t.Fatal("expected an error for an unreadable placement annotation")
	}
	if !strings.Contains(err.Error(), "unmarshal placement") {
		t.Errorf("error = %q, want it to name the unreadable placement", err.Error())
	}
	if len(restored) != 0 {
		t.Errorf("restored = %v, want none", restored)
	}
}

func TestRestorePlacementReportsPatchFailure(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS}}
	kc := fake.NewSimpleClientset(node, sts)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	kc.PrependReactor("patch", "statefulsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	restored, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err == nil {
		t.Fatal("expected the patch failure to surface")
	}
	if !strings.Contains(err.Error(), "patch statefulset") {
		t.Errorf("error = %q, want it to name the failed statefulset patch", err.Error())
	}
	if len(restored) != 0 {
		t.Errorf("restored = %v, want none", restored)
	}
}

func TestValidateNamespace(t *testing.T) {
	cases := []struct {
		ns      string
		wantErr bool
	}{
		{testNS, false},
		{"abc", false},
		{"a", false},
		{"ns1-2", false},
		{"", true},
		{"Foo", true},
		{"foo_bar", true},
		{"foo.bar", true},
		{"../../etc", true},
		{strings.Repeat("a", 64), true},
		{"-foo", true},
		{"foo-", true},
	}
	for _, c := range cases {
		err := k8s.ValidateNamespace(c.ns)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateNamespace(%q) error = %v, wantErr = %v", c.ns, err, c.wantErr)
		}
	}
}

func burstNode(name, ns string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{k8s.PoolLabelKey: ns},
		},
	}
}

func TestWorkloadOnBurstNodes_SpreadAcrossNodes(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		burstNode("burst-2", testNS),
		makePod("p1", testNS, "burst-1", corev1.PodRunning),
		makePod("p2", testNS, "burst-2", corev1.PodRunning),
	)
	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("workload spread across burst nodes must report ready")
	}
}

func TestWorkloadOnBurstNodes_PendingNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("p1", testNS, "burst-1", corev1.PodRunning),
		makePod("p2", testNS, "burst-1", corev1.PodPending),
	)
	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pending pod must not report ready")
	}
}

func TestWorkloadOnBurstNodes_UnlabeledNodeNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "homelab-1"}},
		makePod("p1", testNS, "homelab-1", corev1.PodRunning),
	)
	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pod on an unlabeled node must not report ready")
	}
}

func TestWorkloadOnBurstNodes_NoWorkloadNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testNS))
	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("an empty namespace must not report ready")
	}
}

func TestWorkloadOnBurstNodes_DaemonSetIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("app", testNS, "burst-1", corev1.PodRunning),
		makeDSPod("ds-pod", testNS, "homelab-1"),
	)
	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a daemonset pod off the burst nodes must not hold the check back")
	}
}

func TestWorkloadOnBurstNodes_EmptyNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, ""); err == nil {
		t.Fatal("expected error for empty namespace, got nil")
	}
}

func TestWorkloadOnBurstNodes_ListErrorSurfaces(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testNS))
	kc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS); err == nil {
		t.Fatal("expected the pod list failure to surface instead of a false negative")
	}
}

func reservedNode(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{k8s.PoolLabelKey: "reserved"}},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func TestReservedNodesReady_Satisfied(t *testing.T) {
	kc := fake.NewSimpleClientset(reservedNode("reserved-1", true), reservedNode("reserved-2", true))
	ready, err := k8s.ReservedNodesReady(context.Background(), kc, "reserved", 2)
	if err != nil {
		t.Fatalf("ReservedNodesReady: %v", err)
	}
	if !ready {
		t.Error("two ready reserved nodes must satisfy a want of two")
	}
}

func TestReservedNodesReady_NotYetReady(t *testing.T) {
	kc := fake.NewSimpleClientset(reservedNode("reserved-1", false), burstNode("burst-1", "burst"))
	ready, err := k8s.ReservedNodesReady(context.Background(), kc, "reserved", 1)
	if err != nil {
		t.Fatalf("ReservedNodesReady: %v", err)
	}
	if ready {
		t.Error("a node that is not Ready must not count")
	}
}

func TestReservedNodesReady_EmptyPoolValue(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.ReservedNodesReady(context.Background(), kc, "", 1); err == nil {
		t.Fatal("expected error for empty pool value")
	}
}

func TestReservedNodesReady_ListErrorSurfaces(t *testing.T) {
	kc := fake.NewSimpleClientset()
	kc.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := k8s.ReservedNodesReady(context.Background(), kc, "reserved", 1); err == nil {
		t.Fatal("expected the node list failure to surface instead of a false negative")
	}
}
