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
	corev1 "k8s.io/api/core/v1"
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
	if want := k8s.LeaseNodeAffinity(testLease.UID); !reflect.DeepEqual(podSpec.Affinity, want) {
		t.Errorf("the copy's affinity is %+v, want it pinned to the lease's nodes", podSpec.Affinity)
	}
	if want := k8s.WithBurstToleration(original.Spec.Template.Spec.Tolerations, testLease.Name); !reflect.DeepEqual(podSpec.Tolerations, want) {
		t.Errorf("the copy's tolerations %+v do not tolerate the burst taint", podSpec.Tolerations)
	}
	if podSpec.NodeSelector != nil {
		t.Errorf("the copy keeps the node selector %v, which no leased node carries", podSpec.NodeSelector)
	}
	if copied.Spec.Paused {
		t.Error("the copy is paused, so it never places a pod on the capacity the lease rents")
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
