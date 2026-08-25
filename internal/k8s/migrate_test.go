package k8s_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	poolValue          = "burst"
	testNS             = "sentio-systems"
	emptyPlacementJSON = "{}"
)

func patchCount(kc *fake.Clientset, resource, name string) int {
	count := 0
	for _, a := range kc.Actions() {
		if a.GetVerb() != "patch" || a.GetResource().Resource != resource {
			continue
		}
		pa, ok := a.(k8stesting.PatchAction)
		if !ok || (name != "" && pa.GetName() != name) {
			continue
		}
		count++
	}
	return count
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

func evictionNames(kc *fake.Clientset) []string {
	var names []string
	for _, a := range kc.Actions() {
		if a.GetVerb() != "create" || a.GetSubresource() != "eviction" {
			continue
		}
		create, ok := a.(k8stesting.CreateAction)
		if !ok {
			continue
		}
		if ev, ok := create.GetObject().(interface{ GetName() string }); ok {
			names = append(names, ev.GetName())
		}
	}
	return names
}

func getDeployment(t *testing.T, kc *fake.Clientset, name string) *appsv1.Deployment {
	t.Helper()
	dep, err := kc.AppsV1().Deployments(testNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment %q: %v", name, err)
	}
	return dep
}

func getStatefulSet(t *testing.T, kc *fake.Clientset, name string) *appsv1.StatefulSet {
	t.Helper()
	sts, err := kc.AppsV1().StatefulSets(testNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset %q: %v", name, err)
	}
	return sts
}

func burstNodeAffinity() *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      k8s.PoolLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{poolValue},
					}},
				}},
			},
		},
	}
}

func burstToleration() corev1.Toleration {
	return corev1.Toleration{
		Key:      k8s.BurstTaintKey,
		Operator: corev1.TolerationOpEqual,
		Effect:   corev1.TaintEffectNoSchedule,
	}
}

func hostSpreadAffinity(weight int32) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight:          weight,
				PodAffinityTerm: corev1.PodAffinityTerm{TopologyKey: "kubernetes.io/hostname"},
			}},
		},
	}
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

	evicted := evictionNames(kc)
	if len(evicted) != 1 {
		t.Fatalf("eviction count = %d, want 1", len(evicted))
	}
	if evicted[0] != "app-pod" {
		t.Errorf("evicted pod = %q, want app-pod", evicted[0])
	}
}

func TestMigrateLeavesDeploymentPodsToItsRollout(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
	}
	matched := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "web"}
	unmatched := makePod("other-pod", testNS, "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, matched, unmatched)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "other-pod" {
		t.Errorf("evicted = %v, want [other-pod]: the deployment's own pod belongs to its rollout", evicted)
	}
}

func TestMigrateLeavesStatefulSetPodsToItsRollout(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
		},
	}
	matched := makePod("sts1-0", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "db"}

	kc := fake.NewSimpleClientset(node, sts, matched)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: the statefulset's own pod belongs to its rollout", evicted)
	}
}

func TestRestorePlacementLeavesDeploymentPodsToItsRollout(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "app",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: emptyPlacementJSON},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
	}
	matched := makePod("app-pod", testNS, "burst-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "web"}
	unmatched := makePod("other-pod", testNS, "burst-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(dep, matched, unmatched)
	evictAndDelete(kc)

	if _, err := k8s.RestorePlacement(context.Background(), kc, testNS); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "other-pod" {
		t.Errorf("evicted = %v, want [other-pod]: the deployment's own pod belongs to its rollout", evicted)
	}
}

func TestMigrateRejectsEmptyDeploymentSelector(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{}},
	}
	pod := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, pod)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err == nil {
		t.Fatal("expected an error for a deployment with an empty selector")
	}
	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: an empty selector must not silently suppress eviction", evicted)
	}
}

func TestMigrateRejectsEmptyStatefulSetSelector(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS},
		Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{}},
	}
	pod := makePod("sts1-0", testNS, "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, sts, pod)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err == nil {
		t.Fatal("expected an error for a statefulset with an empty selector")
	}
	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: an empty selector must not silently suppress eviction", evicted)
	}
}

func TestMigrateEvictsOnDeleteStatefulSetPods(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS},
		Spec: appsv1.StatefulSetSpec{
			Selector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType},
		},
	}
	matched := makePod("sts1-0", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "db"}

	kc := fake.NewSimpleClientset(node, sts, matched)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "sts1-0" {
		t.Errorf("evicted = %v, want [sts1-0]: an OnDelete statefulset never rolls its pods on its own", evicted)
	}
}

func TestMigrateEvictsStatefulSetPodsBelowPartition(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	partition := int32(2)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type:          appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: &partition},
			},
		},
	}
	matched := makePod("sts1-0", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "db"}

	kc := fake.NewSimpleClientset(node, sts, matched)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "sts1-0" {
		t.Errorf("evicted = %v, want [sts1-0]: a non-zero partition leaves pods below it unrolled", evicted)
	}
}

func TestMigrateEvictsPausedDeploymentPods(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Paused:   true,
		},
	}
	matched := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "web"}

	kc := fake.NewSimpleClientset(node, dep, matched)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "app-pod" {
		t.Errorf("evicted = %v, want [app-pod]: a paused deployment never rolls its pods on its own", evicted)
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

	cases := []struct {
		resource string
		podSpec  func() corev1.PodSpec
	}{
		{"deployments", func() corev1.PodSpec { return getDeployment(t, kc, "dep1").Spec.Template.Spec }},
		{"statefulsets", func() corev1.PodSpec { return getStatefulSet(t, kc, "sts1").Spec.Template.Spec }},
	}
	for _, c := range cases {
		kinds := patchTypes(kc, c.resource)
		if len(kinds) != 1 {
			t.Errorf("%s patch count = %d, want 1", c.resource, len(kinds))
			continue
		}
		if kinds[0] != types.StrategicMergePatchType {
			t.Errorf("%s patch type = %v, want StrategicMergePatchType", c.resource, kinds[0])
		}
		spec := c.podSpec()
		if !reflect.DeepEqual(spec.Affinity, burstNodeAffinity()) {
			t.Errorf("%s affinity = %+v, want %+v", c.resource, spec.Affinity, burstNodeAffinity())
		}
		if !reflect.DeepEqual(spec.Tolerations, []corev1.Toleration{burstToleration()}) {
			t.Errorf("%s tolerations = %+v, want only the burst toleration", c.resource, spec.Tolerations)
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

	if got := patchCount(kc, "deployments", "dep1"); got != 1 {
		t.Fatalf("dep1 patch count = %d, want 1: the record must ride the affinity rewrite", got)
	}

	stored := getDeployment(t, kc, "dep1")
	if got := stored.Annotations[k8s.PrePlacementAnnotationKey]; got != emptyPlacementJSON {
		t.Errorf("placement annotation = %q, want %q", got, emptyPlacementJSON)
	}
	if got := stored.Labels[k8s.BurstPlacementLabelKey]; got != k8s.BurstPlacementLabelValue {
		t.Errorf("placement label = %q, want %q", got, k8s.BurstPlacementLabelValue)
	}
	if got := stored.Spec.Template.Spec.Affinity; !reflect.DeepEqual(got, burstNodeAffinity()) {
		t.Errorf("affinity = %+v, want %+v", got, burstNodeAffinity())
	}
}

func TestMigrateSavedPlacementHoldsOriginalAffinityAndTolerations(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	originalAffinity := hostSpreadAffinity(100)
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

func TestMigrateDoesNotRepatchAnAlreadyMigratedWorkload(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: emptyPlacementJSON},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	migrated, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 1 || migrated[0] != "deployment/dep1" {
		t.Errorf("migrated = %v, want [deployment/dep1]: the workload sits on burst capacity", migrated)
	}
	if got := patchCount(kc, "deployments", "dep1"); got != 0 {
		t.Errorf("dep1 patched %d times, which would overwrite the saved placement", got)
	}
	if got := getDeployment(t, kc, "dep1").Annotations[k8s.PrePlacementAnnotationKey]; got != emptyPlacementJSON {
		t.Errorf("placement annotation = %q, want it left untouched", got)
	}
}

func TestMigrateReportsTheSameWorkloadsOnASecondPass(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: hostSpreadAffinity(100)}},
		},
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS}}
	pod := makePod("dep1-pod", testNS, "homelab-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(node, dep, sts, pod)
	evictAndDelete(kc)

	want := []string{"deployment/dep1", "statefulset/sts1"}
	first, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("first pass reported %v, want %v", first, want)
	}
	saved := getDeployment(t, kc, "dep1").Annotations[k8s.PrePlacementAnnotationKey]
	if _, err := kc.CoreV1().Pods(testNS).Create(context.Background(), makePod("dep1-pod-2", testNS, "burst-1", corev1.PodRunning), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create the replacement pod: %v", err)
	}
	kc.ClearActions()

	second, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if !reflect.DeepEqual(second, want) {
		t.Errorf("second pass reported %v, want the same %v", second, want)
	}
	if got := patchCount(kc, "deployments", "dep1"); got != 0 {
		t.Errorf("dep1 patched %d times on the second pass, want none", got)
	}
	if got := getDeployment(t, kc, "dep1").Annotations[k8s.PrePlacementAnnotationKey]; got != saved {
		t.Errorf("placement annotation = %q, want the original %q", got, saved)
	}
	if got := evictionNames(kc); len(got) != 0 {
		t.Errorf("second pass evicted %v, want no eviction when nothing changed", got)
	}
}

func TestMigrateReportsNoWorkloadsForAnEmptyNamespace(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	elsewhere := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"}}
	kc := fake.NewSimpleClientset(node, elsewhere)
	evictAndDelete(kc)

	migrated, err := k8s.Migrate(context.Background(), kc, testNS, poolValue)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 0 {
		t.Errorf("migrated = %v, want none", migrated)
	}
	if got := evictionNames(kc); len(got) != 0 {
		t.Errorf("evicted %v, want no eviction for a namespace without workloads", got)
	}
}

func TestRestorePlacementFollowsRepeatedMigratePasses(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	originalAffinity := hostSpreadAffinity(100)
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

	for pass := range 2 {
		if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
			t.Fatalf("Migrate pass %d: %v", pass+1, err)
		}
	}

	restored, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "deployment/dep1" {
		t.Fatalf("restored = %v, want [deployment/dep1]", restored)
	}

	stored := getDeployment(t, kc, "dep1")
	if got := stored.Spec.Template.Spec.Affinity; !reflect.DeepEqual(got, originalAffinity) {
		t.Errorf("affinity = %+v, want exactly the original %+v", got, originalAffinity)
	}
	if got := stored.Spec.Template.Spec.Tolerations; !reflect.DeepEqual(got, []corev1.Toleration{originalToleration}) {
		t.Errorf("tolerations = %+v, want only the original %+v", got, originalToleration)
	}
	if _, ok := stored.Annotations[k8s.PrePlacementAnnotationKey]; ok {
		t.Error("restore must remove the placement annotation")
	}
}

func TestMigrateSavesOriginalAffinity(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}

	originalAffinity := hostSpreadAffinity(100)

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

	for _, name := range []string{"dep1", "dep2"} {
		if got := patchCount(kc, "deployments", name); got != 1 {
			t.Fatalf("%s restore patch count = %d, want 1", name, got)
		}
	}

	restored := getDeployment(t, kc, "dep1")
	if got := restored.Spec.Template.Spec.Affinity; !reflect.DeepEqual(got, originalAffinity) {
		t.Errorf("dep1 affinity = %+v, want exactly the original %+v", got, originalAffinity)
	}
	if got := restored.Spec.Template.Spec.Tolerations; len(got) != 0 {
		t.Errorf("dep1 tolerations = %+v, want none: the burst toleration must go", got)
	}
	cleared := getDeployment(t, kc, "dep2")
	if got := cleared.Spec.Template.Spec.Affinity; got != nil {
		t.Errorf("dep2 affinity = %+v, want nil", got)
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
	existingAffinity := hostSpreadAffinity(50)
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

	migrated := getDeployment(t, kc, "dep1").Spec.Template.Spec
	wantMigrated := []corev1.Toleration{existingToleration, burstToleration()}
	if !reflect.DeepEqual(migrated.Tolerations, wantMigrated) {
		t.Errorf("migrated tolerations = %+v, want %+v", migrated.Tolerations, wantMigrated)
	}
	if !reflect.DeepEqual(migrated.Affinity, burstNodeAffinity()) {
		t.Errorf("migrated affinity = %+v, want %+v", migrated.Affinity, burstNodeAffinity())
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
	if got := patchCount(kc, "deployments", "dep1"); got != 1 {
		t.Fatalf("dep1 restore patch count = %d, want 1", got)
	}

	rolledBack := getDeployment(t, kc, "dep1").Spec.Template.Spec
	wantRolledBack := []corev1.Toleration{existingToleration}
	if !reflect.DeepEqual(rolledBack.Tolerations, wantRolledBack) {
		t.Errorf("restored tolerations = %+v, want %+v", rolledBack.Tolerations, wantRolledBack)
	}
	if rolledBack.Affinity != nil {
		t.Errorf("restored affinity = %+v, want nil: the burst pin must go", rolledBack.Affinity)
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

func TestWorkloadOnBurstNodes_SucceededPodIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("app", testNS, "burst-1", corev1.PodRunning),
		makePod("job-done", testNS, "home-1", corev1.PodSucceeded),
	)
	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a succeeded pod off the burst nodes must not hold the check back")
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

func TestWorkloadOffBurstNodes_ReportsReadyOnlyAfterLeavingBurstNodes(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("p1", testNS, "burst-1", corev1.PodRunning),
	)
	ready, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pod still running on a burst node must not report ready")
	}

	pod, err := kc.CoreV1().Pods(testNS).Get(context.Background(), "p1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	pod.Spec.NodeName = "home-1"
	if _, err := kc.CoreV1().Pods(testNS).Update(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update pod: %v", err)
	}

	ready, err = k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a pod moved off the burst nodes must report ready")
	}
}

func TestWorkloadOffBurstNodes_PendingNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("p1", testNS, "home-1", corev1.PodRunning),
		makePod("p2", testNS, "home-1", corev1.PodPending),
	)
	ready, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pending pod must not report ready")
	}
}

func TestWorkloadOffBurstNodes_NoWorkloadIsReady(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testNS))
	ready, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("an empty namespace has nothing left to restore, so it must report ready")
	}
}

func TestWorkloadOffBurstNodes_DaemonSetIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("app", testNS, "home-1", corev1.PodRunning),
		makeDSPod("ds-pod", testNS, "burst-1"),
	)
	ready, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a daemonset pod on a burst node must not hold the check back")
	}
}

func TestWorkloadOffBurstNodes_SucceededPodIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testNS),
		makePod("app", testNS, "home-1", corev1.PodRunning),
		makePod("job-done", testNS, "burst-1", corev1.PodSucceeded),
	)
	ready, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a succeeded pod stranded on a burst node must not hold the check back")
	}
}

func TestWorkloadOffBurstNodes_EmptyNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, ""); err == nil {
		t.Fatal("expected error for empty namespace, got nil")
	}
}

func TestWorkloadOffBurstNodes_ListErrorSurfaces(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testNS))
	kc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, testNS); err == nil {
		t.Fatal("expected the pod list failure to surface instead of a false negative")
	}
}

func TestMigrateClearsTheNodeSelectorAndSavesIt(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{NodeSelector: map[string]string{"disktype": "ssd", "zone": "a"}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	stored := getDeployment(t, kc, "dep1")
	if got := stored.Spec.Template.Spec.NodeSelector; len(got) != 0 {
		t.Errorf("node selector = %v, want none: a pinned workload cannot reach a burst node", got)
	}

	var saved struct {
		NodeSelector map[string]string `json:"nodeSelector"`
	}
	if err := json.Unmarshal([]byte(stored.Annotations[k8s.PrePlacementAnnotationKey]), &saved); err != nil {
		t.Fatalf("unmarshal placement annotation: %v", err)
	}
	want := map[string]string{"disktype": "ssd", "zone": "a"}
	if !reflect.DeepEqual(saved.NodeSelector, want) {
		t.Errorf("saved node selector = %v, want %v", saved.NodeSelector, want)
	}
}

func TestRestorePlacementReturnsTheNodeSelector(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	original := map[string]string{"disktype": "ssd"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{NodeSelector: original}},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := k8s.RestorePlacement(context.Background(), kc, testNS); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	if got := getDeployment(t, kc, "dep1").Spec.Template.Spec.NodeSelector; !reflect.DeepEqual(got, original) {
		t.Errorf("node selector = %v, want %v", got, original)
	}
}

func TestRestorePlacementDropsNodeSelectorKeysAddedWhileOnBurst(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: map[string]string{k8s.PoolLabelKey: poolValue}}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{NodeSelector: map[string]string{"disktype": "ssd"}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, poolValue); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	onBurst := getDeployment(t, kc, "dep1")
	onBurst.Spec.Template.Spec.NodeSelector = map[string]string{"stray": "yes"}
	if _, err := kc.AppsV1().Deployments(testNS).Update(context.Background(), onBurst, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update deployment: %v", err)
	}

	if _, err := k8s.RestorePlacement(context.Background(), kc, testNS); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	want := map[string]string{"disktype": "ssd"}
	if got := getDeployment(t, kc, "dep1").Spec.Template.Spec.NodeSelector; !reflect.DeepEqual(got, want) {
		t.Errorf("node selector = %v, want %v: restore must replace the map rather than merge into it", got, want)
	}
}

func TestRestorePlacementLeavesTheNodeSelectorOfAnOlderAnnotation(t *testing.T) {
	pinned := map[string]string{"disktype": "ssd"}
	legacy := `{"affinity":null,"tolerations":[{"key":"workload","operator":"Exists"}]}`
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: legacy},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{NodeSelector: pinned}},
		},
	}
	kc := fake.NewSimpleClientset(dep)
	evictAndDelete(kc)

	restored, err := k8s.RestorePlacement(context.Background(), kc, testNS)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "deployment/dep1" {
		t.Fatalf("restored = %v, want [deployment/dep1]", restored)
	}

	stored := getDeployment(t, kc, "dep1")
	if got := stored.Spec.Template.Spec.NodeSelector; !reflect.DeepEqual(got, pinned) {
		t.Errorf("node selector = %v, want %v: an annotation written before node selectors were saved must not erase one", got, pinned)
	}
	if len(stored.Spec.Template.Spec.Tolerations) != 1 {
		t.Errorf("tolerations = %v, want the one the older annotation saved", stored.Spec.Template.Spec.Tolerations)
	}
}

func TestWithBurstTolerationIsValued(t *testing.T) {
	got := k8s.WithBurstToleration(nil, "lease-a")
	if len(got) != 1 {
		t.Fatalf("toleration count = %d, want 1", len(got))
	}
	if got[0].Operator != corev1.TolerationOpEqual {
		t.Errorf("operator = %v, want Equal", got[0].Operator)
	}
	if got[0].Value != "lease-a" {
		t.Errorf("value = %q, want %q", got[0].Value, "lease-a")
	}
}

func TestWithBurstTolerationRejectsForeignLease(t *testing.T) {
	foreign := []corev1.Toleration{{
		Key:      k8s.BurstTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    "lease-b",
		Effect:   corev1.TaintEffectNoSchedule,
	}}
	got := k8s.WithBurstToleration(foreign, "lease-a")
	if len(got) != 2 {
		t.Fatalf("toleration count = %d, want 2, a foreign lease's toleration must not satisfy this lease", len(got))
	}
}
