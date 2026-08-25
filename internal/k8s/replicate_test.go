package k8s_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const testBurstReplicas = 3

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

	name := k8s.BurstCopy(original, testLease, testBurstReplicas).Name
	again := k8s.BurstCopy(original, testLease, testBurstReplicas).Name
	other := k8s.BurstCopy(original, k8s.LeaseIdentity{UID: "uid-b", Name: "lease-b"}, testBurstReplicas).Name

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

	name := k8s.BurstCopy(original, testLease, testBurstReplicas).Name

	if len(name) > nameLimit {
		t.Errorf("copy name is %d characters, want no more than %d", len(name), nameLimit)
	}
	if !strings.Contains(name, "-burst-") {
		t.Errorf("copy name %q lost the burst infix to the trim", name)
	}
}

func TestBurstCopySelectorNamesOnlyItsOwnPods(t *testing.T) {
	original := originalDeployment()

	copied := k8s.BurstCopy(original, testLease, testBurstReplicas)

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

	copied := k8s.BurstCopy(original, testLease, testBurstReplicas)

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

	copied := k8s.BurstCopy(original, testLease, testBurstReplicas)
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

	copied := k8s.BurstCopy(original, testLease, testBurstReplicas)

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

func TestBurstCopyLeavesTheOriginalAlone(t *testing.T) {
	original := originalDeployment()
	before := original.DeepCopy()

	k8s.BurstCopy(original, testLease, testBurstReplicas)

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
