package k8s_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
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
	testNS              = "sentio-systems"
	testNSB             = "sentio-systems-b"
	emptyPlacementJSON  = "{}"
	pinnedPlacementJSON = `{"nodeSelector":{"disktype":"ssd"}}`
	hostSpreadWeight    = int32(100)
	noEvictionRetry     = 0
	evictionRetryBudget = 40 * time.Millisecond
)

func appSelector(app string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}}
}

func nsSet(t *testing.T, namespaces ...string) k8s.TargetSet {
	t.Helper()
	set, err := k8s.NewNamespaceSet(namespaces)
	if err != nil {
		t.Fatalf("NewNamespaceSet(%v): %v", namespaces, err)
	}
	return set
}

func migrateNS(t *testing.T, kc kubernetes.Interface, namespace string, lease k8s.LeaseIdentity) ([]string, error) {
	t.Helper()
	result, err := k8s.Migrate(context.Background(), kc, nsSet(t, namespace), lease, noEvictionRetry)
	return result.Workloads, err
}

func restoreNS(t *testing.T, kc kubernetes.Interface, namespace string, lease k8s.LeaseIdentity) ([]string, error) {
	t.Helper()
	return k8s.RestorePlacement(context.Background(), kc, nsSet(t, namespace), lease)
}

func classifyNS(t *testing.T, kc kubernetes.Interface, namespace string, lease k8s.LeaseIdentity) ([]k8s.WorkloadMigratability, error) {
	t.Helper()
	return k8s.ClassifyMigratability(context.Background(), kc, nsSet(t, namespace), lease)
}

func onBurstNS(t *testing.T, kc kubernetes.Interface, namespace string, lease k8s.LeaseIdentity) (bool, error) {
	t.Helper()
	return k8s.WorkloadOnBurstNodes(context.Background(), kc, nsSet(t, namespace), lease)
}

func offBurstNS(t *testing.T, kc kubernetes.Interface, namespace string, lease k8s.LeaseIdentity) (bool, error) {
	t.Helper()
	return k8s.WorkloadOffBurstNodes(context.Background(), kc, nsSet(t, namespace), lease)
}

var testLease = k8s.LeaseIdentity{UID: "uid-a", Name: "lease-a"}

func burstNode(name, leaseUID string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{provider.LeaseUIDLabelKey: leaseUID},
		},
	}
}

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
						Key:      provider.LeaseUIDLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{testLease.UID},
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
		Value:    testLease.Name,
		Effect:   corev1.TaintEffectNoSchedule,
	}
}

func hostSpreadAffinity() *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight:          hostSpreadWeight,
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
		names = append(names, deps.Items[i].Namespace+"/deployment/"+deps.Items[i].Name)
	}
	stss, err := kc.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, opts)
	if err != nil {
		t.Fatalf("list statefulsets by burst label: %v", err)
	}
	for i := range stss.Items {
		names = append(names, stss.Items[i].Namespace+"/statefulset/"+stss.Items[i].Name)
	}
	return names
}

func plainDeployment(name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       appsv1.DeploymentSpec{Selector: appSelector(name)},
	}
}

func plainStatefulSet(name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       appsv1.StatefulSetSpec{Selector: appSelector(name)},
	}
}

func pausedDeployment(name, app string) *appsv1.Deployment {
	return pausedDeploymentIn(testNS, name, app)
}

func pausedDeploymentIn(namespace, name, app string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}},
			Paused:   true,
		},
	}
}

func rollingDeploymentIn(namespace, name, app string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}},
		},
	}
}

func podIn(namespace, name, app, node string) *corev1.Pod {
	pod := makePod(name, namespace, node, corev1.PodRunning)
	pod.Labels = map[string]string{"app": app}
	return pod
}

func migratedMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:        name,
		Namespace:   testNS,
		Annotations: map[string]string{k8s.PrePlacementAnnotationKey: emptyPlacementJSON},
		Labels: map[string]string{
			k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue,
			provider.LeaseUIDLabelKey:  testLease.UID,
		},
	}
}

func labelledPod(name, app, node string, phase corev1.PodPhase) *corev1.Pod {
	pod := makePod(name, testNS, node, phase)
	pod.Labels = map[string]string{"app": app}
	return pod
}

func createPod(t *testing.T, kc *fake.Clientset, name, app, node string) {
	t.Helper()
	pod := labelledPod(name, app, node, corev1.PodRunning)
	if _, err := kc.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod %q: %v", name, err)
	}
}

func ownedDeployment(name, app string, lease k8s.LeaseIdentity) *appsv1.Deployment {
	meta := migratedMeta(name)
	meta.Labels[provider.LeaseUIDLabelKey] = lease.UID
	return &appsv1.Deployment{
		ObjectMeta: meta,
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}},
		},
	}
}

func makeJobPod(name, node string) *corev1.Pod {
	pod := labelledPod(name, "batch-run", node, corev1.PodRunning)
	pod.OwnerReferences = []metav1.OwnerReference{{
		Kind:       "Job",
		Name:       "batch",
		APIVersion: "batch/v1",
		UID:        "j",
		Controller: boolPtr(true),
	}}
	return pod
}

func TestMigrateEviction(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := pausedDeployment("app", "web")
	appPod := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	appPod.Labels = map[string]string{"app": "web"}
	dsPod := makeDSPod("ds-pod", testNS, "homelab-1")
	dsPod.Labels = map[string]string{"app": "web"}
	otherPod := makePod("other-pod", "default", "homelab-1", corev1.PodRunning)
	otherPod.Labels = map[string]string{"app": "web"}

	kc := fake.NewSimpleClientset(node, dep, appPod, dsPod, otherPod)
	evictAndDelete(kc)

	migrated, err := migrateNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 1 || migrated[0] != "sentio-systems/deployment/app" {
		t.Errorf("migrated = %v, want [sentio-systems/deployment/app]", migrated)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 {
		t.Fatalf("eviction count = %d, want 1", len(evicted))
	}
	if evicted[0] != "app-pod" {
		t.Errorf("evicted pod = %q, want app-pod", evicted[0])
	}
}

func TestMigrateEvictsOnlyThePodsOfTheWorkloadsItPatched(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := pausedDeployment("app", "web")
	owned := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	owned.Labels = map[string]string{"app": "web"}
	bare := makePod("bare-pod", testNS, "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, owned, bare)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "app-pod" {
		t.Errorf("evicted = %v, want [app-pod]: nothing would recreate a pod with no controlling workload", evicted)
	}
}

func TestMigrateLeavesDeploymentPodsToItsRollout(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	rolling := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
	}
	stalled := pausedDeployment("batch", "batch")
	matched := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "web"}
	unmatched := makePod("other-pod", testNS, "homelab-1", corev1.PodRunning)
	unmatched.Labels = map[string]string{"app": "batch"}

	kc := fake.NewSimpleClientset(node, rolling, stalled, matched, unmatched)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "other-pod" {
		t.Errorf("evicted = %v, want [other-pod]: the rolling deployment's own pod belongs to its rollout", evicted)
	}
}

func TestMigrateLeavesStatefulSetPodsToItsRollout(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
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

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: the statefulset's own pod belongs to its rollout", evicted)
	}
}

func TestRestorePlacementLeavesDeploymentPodsToItsRollout(t *testing.T) {
	rolling := &appsv1.Deployment{
		ObjectMeta: migratedMeta("app"),
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
	}
	stalled := &appsv1.Deployment{
		ObjectMeta: migratedMeta("batch"),
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "batch"}},
			Paused:   true,
		},
	}
	matched := makePod("app-pod", testNS, "burst-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "web"}
	unmatched := makePod("other-pod", testNS, "burst-1", corev1.PodRunning)
	unmatched.Labels = map[string]string{"app": "batch"}

	kc := fake.NewSimpleClientset(rolling, stalled, matched, unmatched)
	evictAndDelete(kc)

	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "other-pod" {
		t.Errorf("evicted = %v, want [other-pod]: the rolling deployment's own pod belongs to its rollout", evicted)
	}
}

func TestMigrateRejectsAWorkloadThatNamesNoSelectorAtAll(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	cases := map[string]runtime.Object{
		"deployment":  &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS}},
		"statefulset": &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS}},
	}
	for kind, workload := range cases {
		t.Run(kind, func(t *testing.T) {
			pod := makePod("stray", testNS, "homelab-1", corev1.PodRunning)
			kc := fake.NewSimpleClientset(node, workload, pod)
			evictAndDelete(kc)

			if _, err := migrateNS(t, kc, testNS, testLease); err == nil {
				t.Fatal("a workload naming no pods was migrated instead of refused")
			}
			if evicted := evictionNames(kc); len(evicted) != 0 {
				t.Errorf("evicted = %v, want none: an absent selector must never stand in for every pod", evicted)
			}
		})
	}
}

func TestWorkloadOnBurstNodesRefusesAWorkloadThatNamesNoSelector(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		&appsv1.Deployment{ObjectMeta: migratedMeta("dep1")},
		labelledPod("stray", "web", "burst-1", corev1.PodRunning),
	)

	if _, err := onBurstNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("an absent selector left the gate answering from every pod in the namespace instead of failing")
	}
}

func TestMigrateRejectsEmptyDeploymentSelector(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{}},
	}
	pod := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, pod)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("expected an error for a deployment with an empty selector")
	}
	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: an empty selector must not silently suppress eviction", evicted)
	}
}

func TestMigrateRejectsEmptyStatefulSetSelector(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: testNS},
		Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{}},
	}
	pod := makePod("sts1-0", testNS, "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, sts, pod)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("expected an error for a statefulset with an empty selector")
	}
	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: an empty selector must not silently suppress eviction", evicted)
	}
}

func TestMigrateEvictsOnDeleteStatefulSetPods(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
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

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "sts1-0" {
		t.Errorf("evicted = %v, want [sts1-0]: an OnDelete statefulset never rolls its pods on its own", evicted)
	}
}

func TestMigrateEvictsStatefulSetPodsBelowPartition(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
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

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "sts1-0" {
		t.Errorf("evicted = %v, want [sts1-0]: a non-zero partition leaves pods below it unrolled", evicted)
	}
}

func TestMigrateEvictsPausedDeploymentPods(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := pausedDeployment("app", "web")
	matched := labelledPod("app-pod", "web", "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, matched)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	evicted := evictionNames(kc)
	if len(evicted) != 1 || evicted[0] != "app-pod" {
		t.Errorf("evicted = %v, want [app-pod]: a paused deployment never rolls its pods on its own", evicted)
	}
}

func TestMigrateDoesNotRelabelNode(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	kc := fake.NewSimpleClientset(node)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" && a.GetResource().Resource == "nodes" {
			t.Errorf("Migrate must not patch nodes, got %v", a)
		}
	}
}

func TestMigrateFailsWhenNoLeaseNode(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "homelab-1"}}
	dep := plainDeployment("app")
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	_, err := migrateNS(t, kc, testNS, testLease)
	if err == nil {
		t.Fatal("expected error when no node carries the lease-uid label")
	}
	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" {
			t.Errorf("Migrate must not mutate workloads before the lease-node check: %v", a)
		}
	}
}

func TestMigrateRejectsInvalidInput(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := migrateNS(t, kc, testNS, k8s.LeaseIdentity{}); err == nil {
		t.Error("expected error for an empty lease identity")
	}
	if _, err := migrateNS(t, kc, testNS, k8s.LeaseIdentity{UID: "uid-a"}); err == nil {
		t.Error("expected error for a lease identity with no name")
	}
	if _, err := migrateNS(t, kc, testNS, k8s.LeaseIdentity{Name: "lease-a"}); err == nil {
		t.Error("expected error for a lease identity with no uid")
	}
}

func TestMigrateRequiresANodeOfThisLease(t *testing.T) {
	foreign := burstNode("burst-b", "uid-b")
	dep := plainDeployment("app")
	kc := fake.NewSimpleClientset(foreign, dep)
	evictAndDelete(kc)

	_, err := migrateNS(t, kc, testNS, k8s.LeaseIdentity{UID: "uid-a", Name: "lease-a"})
	if err == nil {
		t.Fatal("Migrate proceeded with only another lease's node present")
	}
}

func TestMigrateAffinityPatch(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := plainDeployment("dep1")
	sts := plainStatefulSet("sts1")
	kc := fake.NewSimpleClientset(node, dep, sts)
	evictAndDelete(kc)

	migrated, err := migrateNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 2 || migrated[0] != "sentio-systems/deployment/dep1" || migrated[1] != "sentio-systems/statefulset/sts1" {
		t.Errorf("migrated = %v, want [sentio-systems/deployment/dep1 statefulset/sts1]", migrated)
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

func TestLeaseNodeAffinityKeysOnLeaseUID(t *testing.T) {
	got := k8s.LeaseNodeAffinity("uid-a")
	terms := got.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("affinity shape = %+v", terms)
	}
	expr := terms[0].MatchExpressions[0]
	if expr.Key != provider.LeaseUIDLabelKey {
		t.Errorf("key = %q, want %q", expr.Key, provider.LeaseUIDLabelKey)
	}
	if len(expr.Values) != 1 || expr.Values[0] != "uid-a" {
		t.Errorf("values = %v, want [uid-a]", expr.Values)
	}
}

func TestMigrateRecordsPlacementInTheSamePatch(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := plainDeployment("dep1")
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
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

func TestMigrateStampsTheOwningLeaseOnTheWorkload(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := plainDeployment("dep1")
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	stored := getDeployment(t, kc, "dep1")
	if got := stored.Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("owner label = %q, want %q: the marker must name the lease that moved the workload", got, testLease.UID)
	}
	if got := stored.Labels[k8s.BurstPlacementLabelKey]; got != k8s.BurstPlacementLabelValue {
		t.Errorf("placement label = %q, want %q", got, k8s.BurstPlacementLabelValue)
	}
}

func TestRestorePlacementClearsEveryPlacementMarker(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := plainDeployment("dep1")
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	stored := getDeployment(t, kc, "dep1")
	if _, ok := stored.Annotations[k8s.PrePlacementAnnotationKey]; ok {
		t.Error("restore left the placement annotation behind")
	}
	if _, ok := stored.Labels[k8s.BurstPlacementLabelKey]; ok {
		t.Error("restore left the placement label behind")
	}
	if _, ok := stored.Labels[provider.LeaseUIDLabelKey]; ok {
		t.Error("restore left the owner label behind, so a later lease would read the workload as taken")
	}
}

func TestMigrateSavedPlacementHoldsOriginalAffinityAndTolerations(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	originalAffinity := hostSpreadAffinity()
	originalToleration := corev1.Toleration{Key: "workload", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: originalAffinity, Tolerations: []corev1.Toleration{originalToleration}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
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
	node := burstNode("burst-1", testLease.UID)
	dep := ownedDeployment("dep1", "web", testLease)
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	migrated, err := migrateNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 1 || migrated[0] != "sentio-systems/deployment/dep1" {
		t.Errorf("migrated = %v, want [sentio-systems/deployment/dep1]: the workload sits on burst capacity", migrated)
	}
	if got := patchCount(kc, "deployments", "dep1"); got != 0 {
		t.Errorf("dep1 patched %d times, which would overwrite the saved placement", got)
	}
	if got := getDeployment(t, kc, "dep1").Annotations[k8s.PrePlacementAnnotationKey]; got != emptyPlacementJSON {
		t.Errorf("placement annotation = %q, want it left untouched", got)
	}
}

func TestMigrateNeitherPatchesNorClaimsAnotherLeasesWorkload(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	nodeA := burstNode("burst-a", testLease.UID)
	nodeB := burstNode("burst-b", leaseB.UID)
	dep := plainDeployment("app")
	kc := fake.NewSimpleClientset(nodeA, nodeB, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate lease-a: %v", err)
	}
	kc.ClearActions()

	claimed, err := migrateNS(t, kc, testNS, leaseB)
	if err != nil {
		t.Fatalf("Migrate lease-b: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("lease-b claimed %v, want none: lease-a moved that workload", claimed)
	}
	if got := patchCount(kc, "deployments", "app"); got != 0 {
		t.Errorf("lease-b patched app %d times, want none", got)
	}
	if got := getDeployment(t, kc, "app").Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("owner label = %q, want it left at %q", got, testLease.UID)
	}
}

func TestMigrateReadsAnEmptyOwnerLabelAsHeldByNobody(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := ownedDeployment("dep1", "web", k8s.LeaseIdentity{})
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	claimed, err := migrateNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(claimed) != 1 || claimed[0] != "sentio-systems/deployment/dep1" {
		t.Fatalf("claimed = %v, want [sentio-systems/deployment/dep1]: an owner label naming nobody strands the workload for every lease", claimed)
	}
	if got := getDeployment(t, kc, "dep1").Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("owner label = %q, want %q", got, testLease.UID)
	}
}

func TestRestorePlacementReadsAnEmptyOwnerLabelAsHeldByNobody(t *testing.T) {
	dep := ownedDeployment("dep1", "web", k8s.LeaseIdentity{})
	kc := fake.NewSimpleClientset(dep)
	evictAndDelete(kc)

	restored, err := restoreNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "sentio-systems/deployment/dep1" {
		t.Fatalf("restored = %v, want [sentio-systems/deployment/dep1]: an owner label naming nobody leaves the workload pinned for good", restored)
	}
}

func TestMigrateStampsAndClaimsAnAnnotatedWorkloadWithNoOwner(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	liveAffinity := hostSpreadAffinity()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: pinnedPlacementJSON},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("web"),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: liveAffinity}},
		},
	}
	pod := labelledPod("dep1-pod", "web", "burst-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(node, dep, pod)
	evictAndDelete(kc)

	migrated, err := migrateNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 1 || migrated[0] != "sentio-systems/deployment/dep1" {
		t.Errorf("migrated = %v, want [sentio-systems/deployment/dep1]: no other lease holds a marker that names none", migrated)
	}

	stored := getDeployment(t, kc, "dep1")
	if got := stored.Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("owner label = %q, want %q: claiming without stamping leaves the workload uncountable", got, testLease.UID)
	}
	if got := stored.Annotations[k8s.PrePlacementAnnotationKey]; got != pinnedPlacementJSON {
		t.Errorf("placement annotation = %q, want the pre-burst state %q left intact", got, pinnedPlacementJSON)
	}
	if got := stored.Spec.Template.Spec.Affinity; !reflect.DeepEqual(got, liveAffinity) {
		t.Errorf("affinity = %+v, want the live placement %+v left alone", got, liveAffinity)
	}
	if got := evictionNames(kc); len(got) != 0 {
		t.Errorf("evicted %v, want none: stamping an owner moves no pod", got)
	}

	placed, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !placed {
		t.Error("a claimed workload never reaches the readiness gate, so the lease waits until it expires")
	}
}

func TestMigrateReportsTheSameWorkloadsOnASecondPass(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: hostSpreadAffinity()}},
		},
	}
	sts := plainStatefulSet("sts1")
	pod := makePod("dep1-pod", testNS, "homelab-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(node, dep, sts, pod)
	evictAndDelete(kc)

	want := []string{"sentio-systems/deployment/dep1", "sentio-systems/statefulset/sts1"}
	first, err := migrateNS(t, kc, testNS, testLease)
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

	second, err := migrateNS(t, kc, testNS, testLease)
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
	node := burstNode("burst-1", testLease.UID)
	elsewhere := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"}}
	kc := fake.NewSimpleClientset(node, elsewhere)
	evictAndDelete(kc)

	migrated, err := migrateNS(t, kc, testNS, testLease)
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
	node := burstNode("burst-1", testLease.UID)
	originalAffinity := hostSpreadAffinity()
	originalToleration := corev1.Toleration{Key: "workload", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: originalAffinity, Tolerations: []corev1.Toleration{originalToleration}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	for pass := range 2 {
		if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
			t.Fatalf("Migrate pass %d: %v", pass+1, err)
		}
	}

	restored, err := restoreNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "sentio-systems/deployment/dep1" {
		t.Fatalf("restored = %v, want [sentio-systems/deployment/dep1]", restored)
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
	node := burstNode("burst-1", testLease.UID)

	originalAffinity := hostSpreadAffinity()

	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: originalAffinity},
			},
		},
	}
	dep2 := plainDeployment("dep2")

	kc := fake.NewSimpleClientset(node, dep1, dep2)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	kc.ClearActions()

	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
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
	node := burstNode("burst-1", testLease.UID)
	existingAffinity := hostSpreadAffinity()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: existingAffinity}},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	restored, err := restoreNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "sentio-systems/deployment/dep1" {
		t.Fatalf("restored = %v, want [sentio-systems/deployment/dep1]", restored)
	}

	got := getDeployment(t, kc, "dep1").Spec.Template.Spec.Affinity
	if got == nil || got.PodAntiAffinity == nil || got.NodeAffinity != nil {
		t.Errorf("affinity = %+v, want the original podAntiAffinity and no nodeAffinity", got)
	}
}

func TestBurstPlacementLabelFindsMigratedWorkloads(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := plainDeployment("dep1")
	sts := plainStatefulSet("sts1")
	untouched := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "default"}}
	kc := fake.NewSimpleClientset(node, dep, sts, untouched)
	evictAndDelete(kc)

	if got := selectedNames(t, kc); len(got) != 0 {
		t.Fatalf("burst label query before migrate = %v, want none", got)
	}

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got := selectedNames(t, kc)
	want := []string{"sentio-systems/deployment/dep1", "sentio-systems/statefulset/sts1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("burst label query = %v, want %v", got, want)
	}

	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if got := selectedNames(t, kc); len(got) != 0 {
		t.Errorf("burst label query after restore = %v, want none", got)
	}
}

func TestRestorePlacementRestoresTolerationsAndLeavesNode(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)

	existingToleration := corev1.Toleration{Key: "workload", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{existingToleration}},
			},
		},
	}
	sts1 := plainStatefulSet("sts1")

	kc := fake.NewSimpleClientset(node, dep1, sts1)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
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

	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
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
	if n.Labels[provider.LeaseUIDLabelKey] != testLease.UID {
		t.Error("node lease label must be left intact after restore")
	}
}

func TestRestorePlacementIsIdempotent(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := plainDeployment("dep1")
	pod := makePod("app-pod", testNS, "burst-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(node, dep, pod)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	first, err := restoreNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("first RestorePlacement: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first restore = %v, want one object", first)
	}
	kc.ClearActions()

	second, err := restoreNS(t, kc, testNS, testLease)
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
	dep := plainDeployment("dep1")
	pod := makePod("app-pod", testNS, "homelab-1", corev1.PodRunning)
	kc := fake.NewSimpleClientset(dep, pod)
	evictAndDelete(kc)

	restored, err := restoreNS(t, kc, testNS, testLease)
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

func TestRestorePlacementRestoresOnlyTheWorkloadsThisLeaseOwns(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	nodeA := burstNode("burst-a", testLease.UID)
	nodeB := burstNode("burst-b", leaseB.UID)
	depA := plainDeployment("app-a")
	kc := fake.NewSimpleClientset(nodeA, nodeB, depA)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate lease-a: %v", err)
	}
	depB := plainDeployment("app-b")
	if _, err := kc.AppsV1().Deployments(testNS).Create(context.Background(), depB, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create app-b: %v", err)
	}
	if _, err := migrateNS(t, kc, testNS, leaseB); err != nil {
		t.Fatalf("Migrate lease-b: %v", err)
	}

	restored, err := restoreNS(t, kc, testNS, leaseB)
	if err != nil {
		t.Fatalf("RestorePlacement lease-b: %v", err)
	}
	if len(restored) != 1 || restored[0] != "sentio-systems/deployment/app-b" {
		t.Fatalf("restored = %v, want [sentio-systems/deployment/app-b]", restored)
	}

	held := getDeployment(t, kc, "app-a")
	if _, ok := held.Annotations[k8s.PrePlacementAnnotationKey]; !ok {
		t.Error("lease-b's teardown restored a workload lease-a still holds")
	}
	if got := held.Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("app-a owner label = %q, want it left at %q", got, testLease.UID)
	}
	if got := getDeployment(t, kc, "app-b").Annotations[k8s.PrePlacementAnnotationKey]; got != "" {
		t.Errorf("app-b placement annotation = %q, want it cleared", got)
	}
}

func TestRestorePlacementRescuesAnAnnotatedWorkloadWithNoOwner(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: emptyPlacementJSON},
			Labels:      map[string]string{k8s.BurstPlacementLabelKey: k8s.BurstPlacementLabelValue},
		},
		Spec: appsv1.DeploymentSpec{Selector: appSelector("web")},
	}
	kc := fake.NewSimpleClientset(dep)
	evictAndDelete(kc)

	restored, err := restoreNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "sentio-systems/deployment/dep1" {
		t.Fatalf("restored = %v, want [sentio-systems/deployment/dep1]: no live lease claims that workload", restored)
	}
	if _, ok := getDeployment(t, kc, "dep1").Annotations[k8s.PrePlacementAnnotationKey]; ok {
		t.Error("an unowned workload stayed pinned to a lease that no longer exists")
	}
}

func TestEvictionSparesTheWorkloadPodsOfAnotherLease(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	nodeA := burstNode("burst-a", testLease.UID)
	nodeB := burstNode("burst-b", leaseB.UID)
	kc := fake.NewSimpleClientset(nodeA, nodeB, pausedDeployment("app-a", "a"))
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate lease-a: %v", err)
	}
	if _, err := kc.AppsV1().Deployments(testNS).Create(context.Background(), pausedDeployment("app-b", "b"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create app-b: %v", err)
	}
	createPod(t, kc, "pod-a", "a", "burst-a")
	createPod(t, kc, "pod-b", "b", "homelab-1")
	kc.ClearActions()

	if _, err := migrateNS(t, kc, testNS, leaseB); err != nil {
		t.Fatalf("Migrate lease-b: %v", err)
	}
	if got := evictionNames(kc); len(got) != 1 || got[0] != "pod-b" {
		t.Fatalf("lease-b's migrate evicted %v, want [pod-b]: lease-a's pod is not its to move", got)
	}

	createPod(t, kc, "pod-b", "b", "burst-b")
	kc.ClearActions()

	if _, err := restoreNS(t, kc, testNS, leaseB); err != nil {
		t.Fatalf("RestorePlacement lease-b: %v", err)
	}
	if got := evictionNames(kc); len(got) != 1 || got[0] != "pod-b" {
		t.Errorf("lease-b's restore evicted %v, want [pod-b]: lease-a's pod must survive another lease's teardown", got)
	}
}

func TestRestorePlacementRejectsAnEmptyIdentity(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := restoreNS(t, kc, testNS, k8s.LeaseIdentity{}); err == nil {
		t.Fatal("RestorePlacement accepted an empty lease identity")
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
		Spec: appsv1.DeploymentSpec{Selector: appSelector("web")},
	}
	kc := fake.NewSimpleClientset(dep)
	evictAndDelete(kc)

	restored, err := restoreNS(t, kc, testNS, testLease)
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
	node := burstNode("burst-1", testLease.UID)
	sts := plainStatefulSet("sts1")
	kc := fake.NewSimpleClientset(node, sts)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	kc.PrependReactor("patch", "statefulsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	restored, err := restoreNS(t, kc, testNS, testLease)
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

func TestNewNamespaceSetRefusesATargetSetNothingCouldRead(t *testing.T) {
	cases := map[string][]string{
		"no namespace at all":          nil,
		"an empty name":                {""},
		"a name with a space":          {"Not Valid"},
		"one bad name among good ones": {"team-a", "Team-B"},
	}
	for name, namespaces := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := k8s.NewNamespaceSet(namespaces); err == nil {
				t.Fatalf("NewNamespaceSet(%v) accepted a set no namespace could be read from", namespaces)
			}
		})
	}
}

func TestNewTargetSetRefusesASelectorThatNamesNoWorkload(t *testing.T) {
	broken := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key:      "tier",
		Operator: "SortOf",
	}}}
	if _, err := k8s.NewTargetSet([]string{testNS}, broken); err == nil {
		t.Fatal("NewTargetSet accepted a selector the apiserver cannot compile")
	}
}

func TestNewTargetSetReadsAnAbsentSelectorAsEveryWorkload(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	kc := fake.NewSimpleClientset(node, plainDeployment("dep1"), plainStatefulSet("sts1"))
	evictAndDelete(kc)

	targets, err := k8s.NewTargetSet([]string{testNS}, nil)
	if err != nil {
		t.Fatalf("NewTargetSet: %v", err)
	}
	result, err := k8s.Migrate(context.Background(), kc, targets, testLease, noEvictionRetry)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := []string{"sentio-systems/deployment/dep1", "sentio-systems/statefulset/sts1"}
	if !reflect.DeepEqual(result.Workloads, want) {
		t.Errorf("migrated = %v, want %v: an absent selector names every workload in the namespace", result.Workloads, want)
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

func TestWorkloadOnBurstNodes_SpreadAcrossNodes(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		burstNode("burst-2", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("p1", "web", "burst-1", corev1.PodRunning),
		labelledPod("p2", "web", "burst-2", corev1.PodRunning),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("workload spread across burst nodes must report ready")
	}
}

func TestWorkloadOnBurstNodes_PendingNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("p1", "web", "burst-1", corev1.PodRunning),
		labelledPod("p2", "web", "burst-1", corev1.PodPending),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pending pod of an owned workload must not report ready")
	}
}

func TestWorkloadOnBurstNodes_UnlabeledNodeNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "homelab-1"}},
		ownedDeployment("app", "web", testLease),
		labelledPod("p1", "web", "homelab-1", corev1.PodRunning),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pod on an unlabeled node must not report ready")
	}
}

func TestWorkloadOnBurstNodes_NoWorkloadNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID))
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("an empty namespace must not report ready")
	}
}

func TestWorkloadOnBurstNodesWithoutAnOwnedWorkloadIsNotReady(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	kc := fake.NewSimpleClientset(
		burstNode("burst-b", leaseB.UID),
		ownedDeployment("app-a", "a", testLease),
		labelledPod("pod-a", "a", "burst-b", corev1.PodRunning),
	)
	ready, err := onBurstNS(t, kc, testNS, leaseB)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("a lease holding no workload has moved nothing and must not report its move complete")
	}
}

func TestWorkloadOnBurstNodes_DaemonSetIgnored(t *testing.T) {
	dsPod := makeDSPod("ds-pod", testNS, "homelab-1")
	dsPod.Labels = map[string]string{"app": "web"}
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "burst-1", corev1.PodRunning),
		dsPod,
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a daemonset pod off the burst nodes must not hold the check back")
	}
}

func TestWorkloadOnBurstNodes_SucceededPodIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "burst-1", corev1.PodRunning),
		labelledPod("job-done", "web", "home-1", corev1.PodSucceeded),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a succeeded pod off the burst nodes must not hold the check back")
	}
}

func TestWorkloadOnBurstNodesIgnoresAnotherLeasesPodsInTheSameNamespace(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	kc := fake.NewSimpleClientset(
		burstNode("burst-a", testLease.UID),
		burstNode("burst-b", leaseB.UID),
		ownedDeployment("app-a", "a", testLease),
		ownedDeployment("app-b", "b", leaseB),
		labelledPod("pod-a", "a", "burst-a", corev1.PodRunning),
		labelledPod("pod-b", "b", "burst-b", corev1.PodRunning),
	)

	readyB, err := onBurstNS(t, kc, testNS, leaseB)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes lease-b: %v", err)
	}
	if !readyB {
		t.Error("lease-b hangs forever counting lease-a's pods against lease-b's nodes")
	}

	readyA, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes lease-a: %v", err)
	}
	if !readyA {
		t.Error("lease-a must still recognise its own workload on its own node")
	}
}

func TestWorkloadOnBurstNodesIgnoresABarePod(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "burst-1", corev1.PodRunning),
		makePod("bare-pod", testNS, "homelab-1", corev1.PodRunning),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a pod no workload controls is never moved, so it must not hold the gate")
	}
}

func TestWorkloadOnBurstNodesIgnoresAJobPod(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "burst-1", corev1.PodRunning),
		makeJobPod("batch-xyz", "homelab-1"),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a job pod matches no migrated workload, so it must not hold the gate")
	}
}

func TestWorkloadOnBurstNodesHoldsWhileAnOwnedPodSitsElsewhere(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "homelab-1", corev1.PodRunning),
	)
	ready, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("an owned workload still off the burst nodes must hold the gate")
	}
}

func TestWorkloadOnBurstNodes_ListErrorSurfaces(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID))
	kc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := onBurstNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("expected the pod list failure to surface instead of a false negative")
	}
}

func TestWorkloadOnBurstNodesReportsAWorkloadListFailure(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID))
	kc.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := onBurstNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("expected the workload list failure to surface instead of a false negative")
	}
}

func TestWorkloadOnBurstNodesIgnoresAnotherLeasesNodes(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-b", "uid-b"),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "burst-b", corev1.PodRunning),
	)

	placed, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if placed {
		t.Fatal("lease-a reported its workload placed while the pod runs on lease-b's node")
	}
}

func TestWorkloadOnBurstNodesAcceptsOwnNodes(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-a", testLease.UID),
		ownedDeployment("app", "web", testLease),
		labelledPod("app-pod", "web", "burst-a", corev1.PodRunning),
	)

	placed, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !placed {
		t.Fatal("lease-a did not recognise its own node")
	}
}

func TestWorkloadOffBurstNodes_ReportsReadyOnlyAfterLeavingBurstNodes(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		makePod("p1", testNS, "burst-1", corev1.PodRunning),
	)
	ready, err := offBurstNS(t, kc, testNS, testLease)
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

	ready, err = offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a pod moved off the burst nodes must report ready")
	}
}

func TestWorkloadOffBurstNodes_PendingPodElsewhereIsReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		makePod("p1", testNS, "home-1", corev1.PodRunning),
		makePod("p2", testNS, "home-1", corev1.PodPending),
	)
	ready, err := offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a pod slow to start away from the burst nodes has already left them and must not stall the release")
	}
}

func TestWorkloadOffBurstNodes_PendingPodOnABurstNodeIsNotReady(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		makePod("p1", testNS, "burst-1", corev1.PodPending),
	)
	ready, err := offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if ready {
		t.Error("a pod still bound to a burst node holds capacity whatever phase it is in")
	}
}

func TestWorkloadOffBurstNodesIgnoresAnotherLeasesNode(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	kc := fake.NewSimpleClientset(
		burstNode("burst-a", testLease.UID),
		burstNode("burst-b", leaseB.UID),
		makePod("pod-b", testNS, "burst-b", corev1.PodRunning),
	)
	ready, err := offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("lease-a's capacity is clear, so another lease's node must not withhold its release")
	}
}

func TestWorkloadOffBurstNodes_NoWorkloadIsReady(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID))
	ready, err := offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("an empty namespace has nothing left to restore, so it must report ready")
	}
}

func TestWorkloadOffBurstNodes_DaemonSetIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		makePod("app", testNS, "home-1", corev1.PodRunning),
		makeDSPod("ds-pod", testNS, "burst-1"),
	)
	ready, err := offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a daemonset pod on a burst node must not hold the check back")
	}
}

func TestWorkloadOffBurstNodes_SucceededPodIgnored(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		makePod("app", testNS, "home-1", corev1.PodRunning),
		makePod("job-done", testNS, "burst-1", corev1.PodSucceeded),
	)
	ready, err := offBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a succeeded pod stranded on a burst node must not hold the check back")
	}
}

func TestWorkloadOffBurstNodes_ListErrorSurfaces(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID))
	kc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := offBurstNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("expected the pod list failure to surface instead of a false negative")
	}
}

func TestMigrateClearsTheNodeSelectorAndSavesIt(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{NodeSelector: map[string]string{"disktype": "ssd", "zone": "a"}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
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
	node := burstNode("burst-1", testLease.UID)
	original := map[string]string{"disktype": "ssd"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{NodeSelector: original}},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}

	if got := getDeployment(t, kc, "dep1").Spec.Template.Spec.NodeSelector; !reflect.DeepEqual(got, original) {
		t.Errorf("node selector = %v, want %v", got, original)
	}
}

func TestRestorePlacementDropsNodeSelectorKeysAddedWhileOnBurst(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("dep1"),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{NodeSelector: map[string]string{"disktype": "ssd"}},
			},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	onBurst := getDeployment(t, kc, "dep1")
	onBurst.Spec.Template.Spec.NodeSelector = map[string]string{"stray": "yes"}
	if _, err := kc.AppsV1().Deployments(testNS).Update(context.Background(), onBurst, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update deployment: %v", err)
	}

	if _, err := restoreNS(t, kc, testNS, testLease); err != nil {
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
			Selector: appSelector("web"),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{NodeSelector: pinned}},
		},
	}
	kc := fake.NewSimpleClientset(dep)
	evictAndDelete(kc)

	restored, err := restoreNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("RestorePlacement: %v", err)
	}
	if len(restored) != 1 || restored[0] != "sentio-systems/deployment/dep1" {
		t.Fatalf("restored = %v, want [sentio-systems/deployment/dep1]", restored)
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

func assertPinnedToOwnLease(t *testing.T, workload string, spec corev1.PodSpec, lease k8s.LeaseIdentity) {
	t.Helper()
	expr := spec.Affinity.NodeAffinity.
		RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0]
	if expr.Key != provider.LeaseUIDLabelKey || len(expr.Values) != 1 || expr.Values[0] != lease.UID {
		t.Errorf("%s affinity = %s in %v, want %s in [%s]", workload, expr.Key, expr.Values, provider.LeaseUIDLabelKey, lease.UID)
	}
	if len(spec.Tolerations) != 1 || spec.Tolerations[0].Value != lease.Name {
		t.Errorf("%s tolerations = %+v, want one valued %s", workload, spec.Tolerations, lease.Name)
	}
	for _, tol := range spec.Tolerations {
		if tol.Key == k8s.BurstTaintKey && tol.Operator == corev1.TolerationOpExists {
			t.Errorf("%s carries a value-blind toleration and would tolerate another lease's taint", workload)
		}
	}
}

func TestTwoLeasesDoNotShareNodes(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}

	nodeA := burstNode("burst-a", testLease.UID)
	nodeB := burstNode("burst-b", leaseB.UID)
	depA := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "a"}},
		},
	}
	depB := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app-b", Namespace: testNSB},
		Spec:       appsv1.DeploymentSpec{Selector: appSelector("b")},
	}
	kc := fake.NewSimpleClientset(nodeA, nodeB, depA, depB)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate lease-a: %v", err)
	}
	if _, err := migrateNS(t, kc, testNSB, leaseB); err != nil {
		t.Fatalf("Migrate lease-b: %v", err)
	}

	createPod(t, kc, "pod-a", "a", "burst-a")
	crossed := ownedDeployment("app-crossed", "crossed", leaseB)
	if _, err := kc.AppsV1().Deployments(testNS).Create(context.Background(), crossed, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create app-crossed: %v", err)
	}
	createPod(t, kc, "pod-crossed", "crossed", "burst-a")

	gotA, err := kc.AppsV1().Deployments(testNS).Get(context.Background(), "app-a", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment app-a: %v", err)
	}
	gotB, err := kc.AppsV1().Deployments(testNSB).Get(context.Background(), "app-b", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment app-b: %v", err)
	}
	assertPinnedToOwnLease(t, "app-a", gotA.Spec.Template.Spec, testLease)
	assertPinnedToOwnLease(t, "app-b", gotB.Spec.Template.Spec, leaseB)

	onA, err := onBurstNS(t, kc, testNS, testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes lease-a: %v", err)
	}
	if !onA {
		t.Fatal("lease-a's readiness gate must count its own pod on its own node")
	}
	onWrongLease, err := onBurstNS(t, kc, testNS, leaseB)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes lease-b against lease-a's namespace: %v", err)
	}
	if onWrongLease {
		t.Fatal("lease-b's readiness gate counted its own workload's pod while that pod sits on lease-a's node")
	}
}

func TestMigrateKeepsEverySelectorInsideItsOwnNamespace(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	kc := fake.NewSimpleClientset(
		node,
		pausedDeploymentIn(testNS, "a-paused", "shared"),
		rollingDeploymentIn(testNS, "a-rolling", "other"),
		rollingDeploymentIn(testNSB, "b-rolling", "shared"),
		pausedDeploymentIn(testNSB, "b-paused", "other"),
		podIn(testNS, "a-shared", "shared", "homelab-1"),
		podIn(testNS, "a-other", "other", "homelab-1"),
		podIn(testNSB, "b-shared", "shared", "homelab-1"),
		podIn(testNSB, "b-other", "other", "homelab-1"),
	)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, nsSet(t, testNS, testNSB), testLease, noEvictionRetry); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got := evictionNames(kc)
	slices.Sort(got)
	want := []string{"a-shared", "b-other"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("evicted = %v, want %v: a label two namespaces share must not let one govern eviction in the other", got, want)
	}
}

func TestMigrateReportsEveryTargetNamespace(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	kc := fake.NewSimpleClientset(
		node,
		rollingDeploymentIn(testNS, "a-app", "a"),
		rollingDeploymentIn(testNSB, "b-app", "b"),
	)
	evictAndDelete(kc)

	result, err := k8s.Migrate(context.Background(), kc, nsSet(t, testNS, testNSB), testLease, noEvictionRetry)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	wantWorkloads := []string{"sentio-systems/deployment/a-app", "sentio-systems-b/deployment/b-app"}
	if !reflect.DeepEqual(result.Workloads, wantWorkloads) {
		t.Errorf("migrated = %v, want %v", result.Workloads, wantWorkloads)
	}
	wantNamespaces := []string{testNS, testNSB}
	if !reflect.DeepEqual(result.MigratedNamespaces, wantNamespaces) {
		t.Errorf("migrated namespaces = %v, want %v", result.MigratedNamespaces, wantNamespaces)
	}
}

func TestMigrateMovesOnlyTheWorkloadsTheSelectorNames(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	batch := rollingDeploymentIn(testNS, "batch-app", "batch")
	batch.Labels = map[string]string{"tier": "batch"}
	kc := fake.NewSimpleClientset(node, batch, rollingDeploymentIn(testNS, "web-app", "web"))
	evictAndDelete(kc)

	targets, err := k8s.NewTargetSet([]string{testNS}, &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "batch"}})
	if err != nil {
		t.Fatalf("NewTargetSet: %v", err)
	}
	result, err := k8s.Migrate(context.Background(), kc, targets, testLease, noEvictionRetry)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	want := []string{"sentio-systems/deployment/batch-app"}
	if !reflect.DeepEqual(result.Workloads, want) {
		t.Errorf("migrated = %v, want %v", result.Workloads, want)
	}
	if got := getDeployment(t, kc, "web-app").Annotations[k8s.PrePlacementAnnotationKey]; got != "" {
		t.Errorf("web-app placement annotation = %q, want it untouched by a selector that names it not", got)
	}
}

func TestClassifyMigratabilityCoversEveryTargetNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset(
		pausedDeploymentIn(testNS, "api", "a"),
		pausedDeploymentIn(testNSB, "api", "b"),
	)

	got, err := k8s.ClassifyMigratability(context.Background(), kc, nsSet(t, testNS, testNSB), testLease)
	if err != nil {
		t.Fatalf("ClassifyMigratability: %v", err)
	}

	want := []k8s.WorkloadMigratability{
		{Workload: "sentio-systems/deployment/api", Verdict: k8s.VerdictDisruptive, Reasons: []string{k8s.ReasonRolloutPaused}},
		{Workload: "sentio-systems-b/deployment/api", Verdict: k8s.VerdictDisruptive, Reasons: []string{k8s.ReasonRolloutPaused}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assessments = %+v, want %+v", got, want)
	}
}

func refuseIn(kc *fake.Clientset, verb, resource, namespace string) {
	kc.PrependReactor(verb, resource, func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() != namespace {
			return false, nil, nil
		}
		return true, nil, errors.New("forbidden")
	})
}

func TestMigrateKeepsWhatMovedWhenOneNamespaceFails(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	kc := fake.NewSimpleClientset(
		node,
		rollingDeploymentIn(testNS, "a-app", "a"),
		rollingDeploymentIn(testNSB, "b-app", "b"),
	)
	evictAndDelete(kc)
	refuseIn(kc, "patch", "deployments", testNSB)

	result, err := k8s.Migrate(context.Background(), kc, nsSet(t, testNS, testNSB), testLease, noEvictionRetry)
	if err == nil {
		t.Fatal("Migrate reported success while one namespace could not be patched")
	}

	wantWorkloads := []string{"sentio-systems/deployment/a-app"}
	if !reflect.DeepEqual(result.Workloads, wantWorkloads) {
		t.Errorf("migrated = %v, want %v: a failing namespace must not discard what the others moved", result.Workloads, wantWorkloads)
	}
	wantNamespaces := []string{testNS}
	if !reflect.DeepEqual(result.MigratedNamespaces, wantNamespaces) {
		t.Errorf("migrated namespaces = %v, want %v", result.MigratedNamespaces, wantNamespaces)
	}
	if !strings.Contains(err.Error(), "b-app") {
		t.Errorf("error = %q, want it to name the workload that stayed", err)
	}
}

func TestRestorePlacementReachesEveryNamespaceWhenOneFails(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	kc := fake.NewSimpleClientset(
		node,
		rollingDeploymentIn(testNS, "a-app", "a"),
		rollingDeploymentIn(testNSB, "b-app", "b"),
	)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, nsSet(t, testNS, testNSB), testLease, noEvictionRetry); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	refuseIn(kc, "patch", "deployments", testNS)

	restored, err := k8s.RestorePlacement(context.Background(), kc, nsSet(t, testNS, testNSB), testLease)
	if err == nil {
		t.Fatal("RestorePlacement reported success while one namespace could not be patched")
	}

	want := []string{"sentio-systems-b/deployment/b-app"}
	if !reflect.DeepEqual(restored, want) {
		t.Errorf("restored = %v, want %v: a namespace that failed must not stop the ones after it", restored, want)
	}
	stored, getErr := kc.AppsV1().Deployments(testNSB).Get(context.Background(), "b-app", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("get b-app: %v", getErr)
	}
	if _, ok := stored.Annotations[k8s.PrePlacementAnnotationKey]; ok {
		t.Error("the namespace after the failing one was never restored")
	}
}

func TestWorkloadOffBurstNodesFoldsEveryNamespaceTogether(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		makePod("stuck", testNSB, "burst-1", corev1.PodRunning),
	)

	ready, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, nsSet(t, testNS, testNSB), testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if ready {
		t.Error("an empty namespace read first masked a namespace still holding burst capacity")
	}

	clear, err := k8s.WorkloadOffBurstNodes(context.Background(), kc, nsSet(t, testNS), testLease)
	if err != nil {
		t.Fatalf("WorkloadOffBurstNodes: %v", err)
	}
	if !clear {
		t.Error("a namespace holding nothing must still report ready on its own")
	}
}

func TestWorkloadOnBurstNodesFoldsEveryNamespaceTogether(t *testing.T) {
	placed := labelledPod("b-pod", "b", "burst-1", corev1.PodRunning)
	placed.Namespace = testNSB
	owned := ownedDeployment("b-app", "b", testLease)
	owned.Namespace = testNSB
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), owned, placed)

	ready, err := k8s.WorkloadOnBurstNodes(context.Background(), kc, nsSet(t, testNS, testNSB), testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if !ready {
		t.Error("a namespace holding no workload of this lease must not hold the gate against one that is placed")
	}

	stray := labelledPod("a-pod", "a", "homelab-1", corev1.PodRunning)
	ownedHere := ownedDeployment("a-app", "a", testLease)
	kc = fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), owned, placed, ownedHere, stray)

	ready, err = k8s.WorkloadOnBurstNodes(context.Background(), kc, nsSet(t, testNS, testNSB), testLease)
	if err != nil {
		t.Fatalf("WorkloadOnBurstNodes: %v", err)
	}
	if ready {
		t.Error("one namespace still off the burst nodes must hold the gate whatever the others report")
	}
}

func refuseEvictions(kc *fake.Clientset, refuse func(name string) bool) {
	kc.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		named, ok := action.(k8stesting.CreateAction).GetObject().(interface{ GetName() string })
		if !ok || !refuse(named.GetName()) {
			return false, nil, nil
		}
		return true, nil, apierrors.NewTooManyRequests("a disruption budget refuses this eviction", 1)
	})
}

func guardedNamespace(t *testing.T) *fake.Clientset {
	t.Helper()
	return fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		pausedDeploymentIn(testNS, "guarded", "guarded"),
		pausedDeploymentIn(testNS, "free", "free"),
		podIn(testNS, "guarded-pod", "guarded", "homelab-1"),
		podIn(testNS, "free-pod", "free", "homelab-1"),
	)
}

func podGone(t *testing.T, kc *fake.Clientset, name string) bool {
	t.Helper()
	_, err := kc.CoreV1().Pods(testNS).Get(context.Background(), name, metav1.GetOptions{})
	return apierrors.IsNotFound(err)
}

func TestMigrateFinishesTheNamespaceWhenADisruptionBudgetRefusesOnePod(t *testing.T) {
	kc := guardedNamespace(t)
	evictAndDelete(kc)
	// refusing whichever pod is reached first is what tells an abort apart from a pass that attempts every pod
	held := ""
	refuseEvictions(kc, func(name string) bool {
		if held == "" {
			held = name
		}
		return name == held
	})

	if _, err := migrateNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("Migrate reported success while a disruption budget held a pod back")
	}

	got := evictionNames(kc)
	slices.Sort(got)
	want := []string{"free-pod", "guarded-pod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evicted = %v, want %v: one refusal must not abandon the rest of the namespace", got, want)
	}
	for _, name := range want {
		if name != held && !podGone(t, kc, name) {
			t.Errorf("%s was left where it was", name)
		}
	}
}

func TestMigrateLeavesATerminalPodAlone(t *testing.T) {
	done := podIn(testNS, "batch-done", "settled", "homelab-1")
	done.Status.Phase = corev1.PodSucceeded
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		pausedDeploymentIn(testNS, "settled", "settled"),
		done,
	)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := evictionNames(kc); len(got) != 0 {
		t.Errorf("evicted = %v, want none: a pod that has finished holds no capacity to move", got)
	}
}

func TestMigrateEvictsAPodADisruptionBudgetHeldBackOnALaterPass(t *testing.T) {
	kc := guardedNamespace(t)
	evictAndDelete(kc)
	guarded := true
	refuseEvictions(kc, func(name string) bool { return guarded && name == "guarded-pod" })

	if _, err := migrateNS(t, kc, testNS, testLease); err == nil {
		t.Fatal("Migrate reported success while a disruption budget held a pod back")
	}
	guarded = false
	kc.ClearActions()

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	if got := patchCount(kc, "deployments", ""); got != 0 {
		t.Fatalf("second pass patched %d deployments, so the retry proves nothing about a pass that patches none", got)
	}
	if got := evictionNames(kc); len(got) != 1 || got[0] != "guarded-pod" {
		t.Errorf("second pass evicted %v, want [guarded-pod]: a half migrated namespace must not be permanent", got)
	}
	if !podGone(t, kc, "guarded-pod") {
		t.Error("the pod a budget once refused was never moved")
	}
}

func TestMigrateRetriesARefusedEvictionWithinItsBudget(t *testing.T) {
	kc := guardedNamespace(t)
	evictAndDelete(kc)
	attempts := 0
	refuseEvictions(kc, func(name string) bool {
		if name != "guarded-pod" {
			return false
		}
		attempts++
		return attempts == 1
	})

	if _, err := k8s.Migrate(context.Background(), kc, nsSet(t, testNS), testLease, evictionRetryBudget); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !podGone(t, kc, "guarded-pod") {
		t.Error("a budget that allows another attempt still left the pod where a single refusal put it")
	}
}

func TestMigrateLeavesAPodOnItsOwnBurstNodeAlone(t *testing.T) {
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", testLease.UID),
		pausedDeploymentIn(testNS, "settled", "settled"),
		podIn(testNS, "settled-pod", "settled", "burst-1"),
	)
	evictAndDelete(kc)

	if _, err := migrateNS(t, kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := evictionNames(kc); len(got) != 0 {
		t.Errorf("evicted = %v, want none: a pod already where it belongs needs no eviction", got)
	}
}
