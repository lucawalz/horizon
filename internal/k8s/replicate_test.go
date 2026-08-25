package k8s_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const testBurstReplicas = 3

func replicationOf(lease k8s.LeaseIdentity) k8s.Replication {
	return k8s.Replication{
		Lease:    lease,
		Replicas: testBurstReplicas,
		Owner: metav1.OwnerReference{
			APIVersion: "horizon.dev/v1alpha1",
			Kind:       "CapacityLease",
			Name:       lease.Name,
			UID:        types.UID(lease.UID),
		},
	}
}

func replicate(t *testing.T, kc kubernetes.Interface, namespaces ...string) (k8s.ReplicationResult, error) {
	t.Helper()
	return k8s.Replicate(context.Background(), kc, nsSet(t, namespaces...), replicationOf(testLease))
}

func burstCopiesIn(t *testing.T, kc kubernetes.Interface, namespace string) []appsv1.Deployment {
	t.Helper()
	list, err := kc.AppsV1().Deployments(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: k8s.BurstCopyLabelKey,
	})
	if err != nil {
		t.Fatalf("list burst copies in %q: %v", namespace, err)
	}
	return list.Items
}

func originalDeployment(mutators ...func(*appsv1.Deployment)) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api",
			Namespace: testNS,
			Labels:    map[string]string{"app": "api", "team": "core"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: appSelector("api"),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "tier": "web"}},
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{{Key: "existing", Operator: corev1.TolerationOpExists}},
				},
			},
		},
	}
	for _, mutate := range mutators {
		mutate(dep)
	}
	return dep
}

func TestBurstCopyNameIsDerivedFromTheLeaseUID(t *testing.T) {
	original := originalDeployment()

	name := k8s.BurstCopy(original, replicationOf(testLease)).Name
	again := k8s.BurstCopy(original, replicationOf(testLease)).Name
	other := k8s.BurstCopy(original, replicationOf(k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"})).Name

	if name != again {
		t.Errorf("two builds of one lease's copy name it %q and %q, want one stable name", name, again)
	}
	if name == other {
		t.Errorf("two leases copy the same workload to %q, so one lease would delete the other's copy", name)
	}
	if !strings.HasPrefix(name, original.Name+"-burst-") {
		t.Errorf("copy name %q does not read as a burst copy of %q", name, original.Name)
	}
}

func TestBurstCopyNameStaysWithinTheObjectNameLimit(t *testing.T) {
	const nameLimit = 253
	original := originalDeployment(func(d *appsv1.Deployment) {
		d.Name = strings.Repeat("a", nameLimit)
	})

	name := k8s.BurstCopy(original, replicationOf(testLease)).Name

	if len(name) > nameLimit {
		t.Errorf("copy name is %d characters, want no more than %d", len(name), nameLimit)
	}
	if !strings.Contains(name, "-burst-") {
		t.Errorf("copy name %q lost the burst infix to the trim", name)
	}
}

func TestBurstCopyNamesTwoLongNamedOriginalsApart(t *testing.T) {
	const nameLimit = 253
	shared := strings.Repeat("a", nameLimit-len("one"))
	first := originalDeployment(func(d *appsv1.Deployment) { d.Name = shared + "one" })
	second := originalDeployment(func(d *appsv1.Deployment) { d.Name = shared + "two" })

	firstCopy := k8s.BurstCopy(first, replicationOf(testLease)).Name
	secondCopy := k8s.BurstCopy(second, replicationOf(testLease)).Name

	if firstCopy == secondCopy {
		t.Errorf("two originals sharing a trimmed prefix both copy to %q, so one of them gets no capacity at all", firstCopy)
	}
}

func TestBurstCopySelectorNamesOnlyItsOwnPods(t *testing.T) {
	original := originalDeployment()

	copied := k8s.BurstCopy(original, replicationOf(testLease))

	selector, err := metav1.LabelSelectorAsSelector(copied.Spec.Selector)
	if err != nil {
		t.Fatalf("compile the copy's selector: %v", err)
	}
	if selector.Matches(labels.Set(original.Spec.Template.Labels)) {
		t.Error("the copy's selector matches the original's pods, so the two replica sets contend over one set of pods")
	}
	if !selector.Matches(labels.Set(copied.Spec.Template.Labels)) {
		t.Error("the copy's selector does not match the copy's own pods")
	}
	original.Spec.Template.Labels[k8s.BurstCopyLabelKey] = testLease.Name
	if !selector.Matches(labels.Set(original.Spec.Template.Labels)) {
		t.Error("the copy's selector drops the original's labels rather than adding to them")
	}
}

func TestBurstCopyPodsAreReachedByTheOriginalService(t *testing.T) {
	original := originalDeployment()

	copied := k8s.BurstCopy(original, replicationOf(testLease))

	service, err := metav1.LabelSelectorAsSelector(original.Spec.Selector)
	if err != nil {
		t.Fatalf("compile the original's selector: %v", err)
	}
	if !service.Matches(labels.Set(copied.Spec.Template.Labels)) {
		t.Error("a service selecting the original does not reach the copy's pods")
	}
}

func TestBurstCopyIsPinnedToTheLeasesNodes(t *testing.T) {
	original := originalDeployment(func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
		d.Spec.Paused = true
	})

	copied := k8s.BurstCopy(original, replicationOf(testLease))
	podSpec := copied.Spec.Template.Spec

	if got := *copied.Spec.Replicas; got != testBurstReplicas {
		t.Errorf("the copy runs %d pods, want %d", got, testBurstReplicas)
	}
	if want := burstNodeAffinity(); !reflect.DeepEqual(podSpec.Affinity, want) {
		t.Errorf("the copy's affinity is %+v, want %+v pinning it to the lease's nodes", podSpec.Affinity, want)
	}
	want := []corev1.Toleration{{Key: "existing", Operator: corev1.TolerationOpExists}, burstToleration()}
	if !reflect.DeepEqual(podSpec.Tolerations, want) {
		t.Errorf("the copy's tolerations are %+v, want %+v", podSpec.Tolerations, want)
	}
	if podSpec.NodeSelector != nil {
		t.Errorf("the copy keeps the node selector %v, which no leased node carries", podSpec.NodeSelector)
	}
	if copied.Spec.Paused {
		t.Error("the copy is paused, so it never places a pod on the capacity the lease rents")
	}
}

func TestBurstCopyKeepsTheOriginalsPodAntiAffinity(t *testing.T) {
	original := originalDeployment(func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.Affinity = hostSpreadAffinity()
	})

	copied := k8s.BurstCopy(original, replicationOf(testLease))
	affinity := copied.Spec.Template.Spec.Affinity

	if !reflect.DeepEqual(affinity.PodAntiAffinity, hostSpreadAffinity().PodAntiAffinity) {
		t.Errorf("the copy's pod anti-affinity is %+v, so every burst replica may land on one node", affinity.PodAntiAffinity)
	}
	if !reflect.DeepEqual(affinity.NodeAffinity, burstNodeAffinity().NodeAffinity) {
		t.Errorf("the copy's node affinity is %+v, want it pinned to the lease's nodes", affinity.NodeAffinity)
	}
}

func TestBurstCopyDropsTheOriginalsPriority(t *testing.T) {
	priority := int32(1000)
	original := originalDeployment(func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.PriorityClassName = "platform-critical"
		d.Spec.Template.Spec.Priority = &priority
	})

	podSpec := k8s.BurstCopy(original, replicationOf(testLease)).Spec.Template.Spec

	if podSpec.PriorityClassName != "" || podSpec.Priority != nil {
		t.Errorf("the copy runs at priority %q/%v, so it preempts pods already on the rented nodes", podSpec.PriorityClassName, podSpec.Priority)
	}
}

func TestBurstCopyCarriesBothOwnershipMarkers(t *testing.T) {
	original := originalDeployment(func(d *appsv1.Deployment) {
		d.Labels[k8s.BurstPlacementLabelKey] = k8s.BurstPlacementLabelValue
		d.Labels[provider.LeaseUIDLabelKey] = "uid-b"
	})

	copied := k8s.BurstCopy(original, replicationOf(testLease))

	if got := copied.Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("the copy names lease %q as its owner, want %q", got, testLease.UID)
	}
	if got := copied.Labels[k8s.BurstCopyLabelKey]; got != testLease.Name {
		t.Errorf("the copy carries burst-copy label %q, want %q", got, testLease.Name)
	}
	if _, marked := copied.Labels[k8s.BurstPlacementLabelKey]; marked {
		t.Error("the copy inherited the placement marker of the lease that moved the original")
	}
	if got := copied.Labels["team"]; got != "core" {
		t.Errorf("the copy carries team label %q, want the original's %q", got, "core")
	}
}

func TestReplicateCreatesOneOwnedCopyPerWorkload(t *testing.T) {
	original := originalDeployment()
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original)

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	copies := burstCopiesIn(t, kc, testNS)
	if len(copies) != 1 {
		t.Fatalf("the namespace holds %d burst copies, want one", len(copies))
	}
	copied := copies[0]
	if want := []string{testNS + "/deployment/" + copied.Name}; !slices.Equal(result.Copies, want) {
		t.Errorf("Replicate reports copies %v, want %v", result.Copies, want)
	}
	if !slices.Equal(result.ReplicatedNamespaces, []string{testNS}) {
		t.Errorf("Replicate reports namespaces %v, want [%s]", result.ReplicatedNamespaces, testNS)
	}
	owners := copied.OwnerReferences
	if len(owners) != 1 || owners[0].Kind != "CapacityLease" || string(owners[0].UID) != testLease.UID {
		t.Errorf("the copy carries owner references %+v, want one naming the lease", owners)
	}
	if got := copied.Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("the copy is labelled with lease %q, want %q", got, testLease.UID)
	}
}

func TestReplicateNeverWritesToTheOriginal(t *testing.T) {
	original := originalDeployment()
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original)

	if _, err := replicate(t, kc, testNS); err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	if got := patchCount(kc, "deployments", original.Name); got != 0 {
		t.Errorf("Replicate patched the original %d times, want none", got)
	}
	stored, err := kc.AppsV1().Deployments(testNS).Get(context.Background(), original.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get the original: %v", err)
	}
	if !reflect.DeepEqual(stored.Spec, original.Spec) {
		t.Errorf("Replicate rewrote the original's spec to %+v", stored.Spec)
	}
	if !maps.Equal(stored.Labels, original.Labels) {
		t.Errorf("Replicate rewrote the original's labels to %v", stored.Labels)
	}
}

func TestReplicateRepeatsWithoutCreatingASecondCopy(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), originalDeployment())

	first, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}

	if !slices.Equal(first.Copies, second.Copies) {
		t.Errorf("the second pass reports %v, want the first pass's %v", second.Copies, first.Copies)
	}
	if got := len(burstCopiesIn(t, kc, testNS)); got != 1 {
		t.Errorf("two passes left %d burst copies, want one", got)
	}
}

func TestReplicateDoesNotCopyABurstCopy(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), originalDeployment())

	if _, err := replicate(t, kc, testNS); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	result, err := k8s.Replicate(context.Background(), kc, nsSet(t, testNS),
		replicationOf(k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}))
	if err == nil {
		t.Fatal("a lease with no node of its own replicated anyway")
	}
	if len(result.Copies) != 0 {
		t.Errorf("the second lease reports copies %v before it holds a node", result.Copies)
	}

	if _, err := kc.CoreV1().Nodes().Create(context.Background(), burstNode("burst-2", "uid-b"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create the second lease's node: %v", err)
	}
	if _, err := k8s.Replicate(context.Background(), kc, nsSet(t, testNS),
		replicationOf(k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"})); err != nil {
		t.Fatalf("second lease: %v", err)
	}

	if got := len(burstCopiesIn(t, kc, testNS)); got != 2 {
		t.Errorf("the namespace holds %d burst copies, want one per lease and none of a copy", got)
	}
}

func autoscalerFor(deployment string) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: deployment, Namespace: testNS},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind:       "Deployment",
				Name:       deployment,
				APIVersion: "apps/v1",
			},
		},
	}
}

func budgetFor(app string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: app, Namespace: testNS},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: appSelector(app)},
	}
}

func warningReasons(warnings []k8s.WorkloadWarning, workload string) []string {
	for _, warning := range warnings {
		if warning.Workload == workload {
			return warning.Reasons
		}
	}
	return nil
}

func TestReplicateSkipsAnAutoscaledWorkloadAndSignpostsMoveMode(t *testing.T) {
	original := originalDeployment()
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original, autoscalerFor(original.Name))

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	if got := len(burstCopiesIn(t, kc, testNS)); got != 0 {
		t.Errorf("an autoscaled workload was copied %d times, and the copy is what scales the original down", got)
	}
	if len(result.Copies) != 0 {
		t.Errorf("Replicate reports copies %v for a workload it must not copy", result.Copies)
	}
	reasons := warningReasons(result.Skipped, testNS+"/deployment/"+original.Name)
	if !slices.Equal(reasons, []string{k8s.ReasonAutoscalerTargeted}) {
		t.Fatalf("the skip reports reasons %v, want [%s]", reasons, k8s.ReasonAutoscalerTargeted)
	}
	if text := k8s.ReplicationReasonText(k8s.ReasonAutoscalerTargeted); !strings.Contains(text, "move mode") {
		t.Errorf("the autoscaler skip reads %q, want it to name move mode as the way to burst the workload", text)
	}
}

func TestReplicateSkipsAWorkloadTheCopyCouldNotBeToldApartFrom(t *testing.T) {
	original := originalDeployment(func(d *appsv1.Deployment) {
		d.Spec.Selector.MatchLabels[k8s.BurstCopyLabelKey] = testLease.Name
		d.Spec.Template.Labels[k8s.BurstCopyLabelKey] = testLease.Name
	})
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original)

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	if got := len(burstCopiesIn(t, kc, testNS)); got != 0 {
		t.Errorf("a workload whose selector the copy cannot narrow was copied %d times", got)
	}
	if len(result.Copies) != 0 {
		t.Errorf("Replicate reports copies %v whose selector names the original's pods as well", result.Copies)
	}
	reasons := warningReasons(result.Skipped, testNS+"/deployment/"+original.Name)
	if !slices.Equal(reasons, []string{k8s.ReasonSelectorUnchanged}) {
		t.Errorf("the skip reports reasons %v, want [%s]", reasons, k8s.ReasonSelectorUnchanged)
	}
}

func spreadOver(topologyKey string, whenUnsatisfiable corev1.UnsatisfiableConstraintAction) func(*appsv1.Deployment) {
	return func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       topologyKey,
			WhenUnsatisfiable: whenUnsatisfiable,
			LabelSelector:     appSelector("api"),
		}}
	}
}

func TestReplicateSkipsAWorkloadWhoseSpreadTheCopyWouldSkew(t *testing.T) {
	original := originalDeployment(spreadOver("kubernetes.io/hostname", corev1.DoNotSchedule))
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original)

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	if got := len(burstCopiesIn(t, kc, testNS)); got != 0 {
		t.Errorf("a workload spreading its pods was copied %d times, and the copy's pods count into the original's own domains", got)
	}
	reasons := warningReasons(result.Skipped, testNS+"/deployment/"+original.Name)
	if !slices.Equal(reasons, []string{k8s.ReasonSpreadSpansCopy}) {
		t.Fatalf("the skip reports reasons %v, want [%s]", reasons, k8s.ReasonSpreadSpansCopy)
	}
	if text := k8s.ReplicationReasonText(k8s.ReasonSpreadSpansCopy); !strings.Contains(text, "move mode") {
		t.Errorf("the spread skip reads %q, want it to name move mode as the way to burst the workload", text)
	}
}

func TestReplicateCopiesAWorkloadWhoseSpreadIsOnlyAPreference(t *testing.T) {
	original := originalDeployment(spreadOver("kubernetes.io/hostname", corev1.ScheduleAnyway))
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original)

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	if got := len(burstCopiesIn(t, kc, testNS)); got != 1 {
		t.Errorf("the namespace holds %d burst copies, want a spread the scheduler only scores on not to cost the workload its capacity", got)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("Replicate skipped %v over a spread that refuses no pod a node", result.Skipped)
	}
}

func TestReplicateSkipsAStatefulSet(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), plainStatefulSet("db"), originalDeployment())

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	reasons := warningReasons(result.Skipped, testNS+"/statefulset/db")
	if !slices.Equal(reasons, []string{k8s.ReasonStatefulSetCopy}) {
		t.Errorf("the skip reports reasons %v, want [%s]", reasons, k8s.ReasonStatefulSetCopy)
	}
	if got := len(burstCopiesIn(t, kc, testNS)); got != 1 {
		t.Errorf("the namespace holds %d burst copies, want only the deployment's", got)
	}
	if result.Matched() != 2 {
		t.Errorf("Replicate matched %d workloads, want the deployment and the statefulset", result.Matched())
	}
}

func TestReplicateWarnsWhenADisruptionBudgetSpansTheCopy(t *testing.T) {
	original := originalDeployment()
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), original, budgetFor("api"))

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	reasons := warningReasons(result.Warnings, testNS+"/deployment/"+original.Name)
	if !slices.Equal(reasons, []string{k8s.ReasonBudgetSpansCopy}) {
		t.Errorf("the warning reports reasons %v, want [%s]", reasons, k8s.ReasonBudgetSpansCopy)
	}
	if got := len(burstCopiesIn(t, kc, testNS)); got != 1 {
		t.Errorf("the namespace holds %d burst copies, want the warning not to have stopped the copy", got)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("a disruption budget skipped %v rather than warning about it", result.Skipped)
	}
}

func TestReplicateCopiesTheNeighboursOfASkippedWorkload(t *testing.T) {
	autoscaled := originalDeployment()
	neighbour := originalDeployment(func(d *appsv1.Deployment) {
		d.Name = "worker"
		d.Spec.Selector = appSelector("worker")
		d.Spec.Template.Labels = map[string]string{"app": "worker"}
	})
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), autoscaled, neighbour, autoscalerFor(autoscaled.Name))

	result, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	copies := burstCopiesIn(t, kc, testNS)
	if len(copies) != 1 || !strings.HasPrefix(copies[0].Name, neighbour.Name) {
		t.Errorf("the namespace holds %d burst copies, want one of %q alone", len(copies), neighbour.Name)
	}
	if len(result.Skipped) != 1 || len(result.Copies) != 1 {
		t.Errorf("Replicate skipped %v and copied %v, want one of each", result.Skipped, result.Copies)
	}
}

func TestDeleteBurstCopiesRemovesOnlyThisLeasesCopies(t *testing.T) {
	moved := ownedDeployment("moved", "moved", testLease)
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), originalDeployment(), moved)
	replicated, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	if err := k8s.DeleteBurstCopies(context.Background(), kc, replicated.Copies, testLease); err != nil {
		t.Fatalf("DeleteBurstCopies: %v", err)
	}

	if got := len(burstCopiesIn(t, kc, testNS)); got != 0 {
		t.Errorf("%d burst copies survived the teardown", got)
	}
	for _, name := range []string{"api", "moved"} {
		if _, err := kc.AppsV1().Deployments(testNS).Get(context.Background(), name, metav1.GetOptions{}); err != nil {
			t.Errorf("teardown deleted the workload %q it only ever read: %v", name, err)
		}
	}
}

func TestDeleteBurstCopiesLeavesAnotherLeasesCopyRunning(t *testing.T) {
	leaseB := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), burstNode("burst-2", leaseB.UID), originalDeployment())
	replicated, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate for the first lease: %v", err)
	}
	if _, err := k8s.Replicate(context.Background(), kc, nsSet(t, testNS), replicationOf(leaseB)); err != nil {
		t.Fatalf("Replicate for the second lease: %v", err)
	}

	if err := k8s.DeleteBurstCopies(context.Background(), kc, replicated.Copies, testLease); err != nil {
		t.Fatalf("DeleteBurstCopies: %v", err)
	}

	copies := burstCopiesIn(t, kc, testNS)
	if len(copies) != 1 || copies[0].Labels[k8s.BurstCopyLabelKey] != leaseB.Name {
		t.Errorf("the namespace holds %d burst copies, want the second lease's alone", len(copies))
	}
}

func TestDeleteBurstCopiesReportsACopyItCannotSelect(t *testing.T) {
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), originalDeployment())
	replicated, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	copied := burstCopiesIn(t, kc, testNS)[0]
	delete(copied.Labels, provider.LeaseUIDLabelKey)
	if _, err := kc.AppsV1().Deployments(testNS).Update(context.Background(), &copied, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("relabel the copy: %v", err)
	}

	err = k8s.DeleteBurstCopies(context.Background(), kc, replicated.Copies, testLease)

	if err == nil {
		t.Fatal("DeleteBurstCopies reported success while a copy it recorded still runs on the lease's nodes")
	}
	if !strings.Contains(err.Error(), copied.Name) {
		t.Errorf("the failure reads %q, want it to name the copy that survived", err)
	}
}

func TestMoveModeLeavesAnotherLeasesBurstCopyAlone(t *testing.T) {
	mover := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), burstNode("burst-2", mover.UID), originalDeployment())
	replication, err := replicate(t, kc, testNS)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	copyName := burstCopiesIn(t, kc, testNS)[0].Name

	moved, err := migrateNS(t, kc, testNS, mover)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if slices.Contains(moved, replication.Copies[0]) {
		t.Errorf("the mover reports %v, which claims a copy another lease owns and deletes", moved)
	}
	stored, err := kc.AppsV1().Deployments(testNS).Get(context.Background(), copyName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get the burst copy: %v", err)
	}
	if _, patched := stored.Annotations[k8s.PrePlacementAnnotationKey]; patched {
		t.Error("the mover saved a placement onto a burst copy, so teardown will restore a workload that no longer exists")
	}
	if got := stored.Labels[provider.LeaseUIDLabelKey]; got != testLease.UID {
		t.Errorf("the burst copy now names lease %q as its owner, want %q", got, testLease.UID)
	}
}

func TestClassifyMigratabilityIgnoresABurstCopy(t *testing.T) {
	mover := k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}
	kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), originalDeployment())
	if _, err := replicate(t, kc, testNS); err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	assessments, err := k8s.ClassifyMigratability(context.Background(), kc, nsSet(t, testNS), mover)
	if err != nil {
		t.Fatalf("ClassifyMigratability: %v", err)
	}

	if len(assessments) != 1 || assessments[0].Workload != testNS+"/deployment/api" {
		t.Errorf("the classifier assessed %+v, want the original alone", assessments)
	}
}

func TestReplicateRefusesAnIncompleteRequest(t *testing.T) {
	tests := []struct {
		name        string
		replication k8s.Replication
	}{
		{"no lease", k8s.Replication{Replicas: testBurstReplicas, Owner: replicationOf(testLease).Owner}},
		{"no replicas", k8s.Replication{Lease: testLease, Owner: replicationOf(testLease).Owner}},
		{"no owner", k8s.Replication{Lease: testLease, Replicas: testBurstReplicas}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kc := fake.NewSimpleClientset(burstNode("burst-1", testLease.UID), originalDeployment())
			if _, err := k8s.Replicate(context.Background(), kc, nsSet(t, testNS), tc.replication); err == nil {
				t.Fatal("Replicate accepted a request it cannot carry out")
			}
			if got := len(burstCopiesIn(t, kc, testNS)); got != 0 {
				t.Errorf("a refused request still left %d copies behind", got)
			}
		})
	}
}

func TestBurstCopyLeavesTheOriginalAlone(t *testing.T) {
	original := originalDeployment()
	before := original.DeepCopy()

	k8s.BurstCopy(original, replicationOf(testLease))

	if !maps.Equal(original.Labels, before.Labels) {
		t.Errorf("building the copy rewrote the original's labels to %v", original.Labels)
	}
	if !maps.Equal(original.Spec.Template.Labels, before.Spec.Template.Labels) {
		t.Errorf("building the copy rewrote the original's pod labels to %v", original.Spec.Template.Labels)
	}
	if !maps.Equal(original.Spec.Selector.MatchLabels, before.Spec.Selector.MatchLabels) {
		t.Errorf("building the copy rewrote the original's selector to %v", original.Spec.Selector.MatchLabels)
	}
	if original.Spec.Replicas != nil {
		t.Errorf("building the copy set the original's replica count to %d", *original.Spec.Replicas)
	}
}
