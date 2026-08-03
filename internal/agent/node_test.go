package agent

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lucawalz/horizon/internal/provider"
)

const testNodeName = "burst-0"

func annotatedNode(deadline string) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: testNodeName}}
	if deadline != "" {
		node.Annotations = map[string]string{provider.WatchdogDeadlineAnnotationKey: deadline}
	}
	return node
}

func nodeDeadlineFor(node *corev1.Node) (*nodeDeadline, *k8sfake.Clientset) {
	return seededNodeDeadlineFor(node, nil)
}

func seededNodeDeadlineFor(node *corev1.Node, seed *time.Time) (*nodeDeadline, *k8sfake.Clientset) {
	client := k8sfake.NewSimpleClientset(node)
	return &nodeDeadline{nodeName: testNodeName, client: client, last: seed}, client
}

func refuseNodeReads(client *k8sfake.Clientset, failure error) {
	client.PrependReactor("get", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, failure
	})
}

func armedDeadline() time.Time {
	return time.Now().Add(time.Hour).UTC().Truncate(time.Second)
}

func assertStuckAt(t *testing.T, deadline *nodeDeadline, want time.Time) {
	t.Helper()
	got := deadline.read(t.Context())
	if got == nil {
		t.Fatal("the agent dropped the last deadline it read")
	}
	if !got.Equal(want) {
		t.Errorf("deadline = %s, want the last good deadline %s", got, want)
	}
}

func TestNodeDeadlineReadsTheAnnotation(t *testing.T) {
	want := armedDeadline()
	deadline, _ := nodeDeadlineFor(annotatedNode(provider.FormatExpiry(want)))

	got := deadline.read(t.Context())
	if got == nil {
		t.Fatal("an annotated node yielded no deadline")
	}
	if !got.Equal(want) {
		t.Errorf("deadline = %s, want %s", got, want)
	}
}

func TestNodeDeadlineIsAbsentUntilTheControllerAnnotatesTheNode(t *testing.T) {
	deadline, _ := nodeDeadlineFor(annotatedNode(""))

	if got := deadline.read(t.Context()); got != nil {
		t.Errorf("deadline = %s, want none while only the backstop is armed", got)
	}
}

func TestNodeDeadlineIgnoresAnUnreadableAnnotation(t *testing.T) {
	deadline, _ := nodeDeadlineFor(annotatedNode("tomorrow"))

	if got := deadline.read(t.Context()); got != nil {
		t.Errorf("deadline = %s, want none for an unreadable annotation", got)
	}
}

func TestNodeDeadlineIsStickyAcrossEveryReadFailure(t *testing.T) {
	failures := map[string]error{
		"the kubelet credential is refused": apierrors.NewForbidden(corev1.Resource("nodes"), testNodeName, errors.New("no permission")),
		"the api server is unreachable":     errors.New("dial tcp 127.0.0.1:6444: connect: connection refused"),
		"the node object is gone":           apierrors.NewNotFound(corev1.Resource("nodes"), testNodeName),
	}

	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			want := armedDeadline()
			deadline, client := nodeDeadlineFor(annotatedNode(provider.FormatExpiry(want)))
			if deadline.read(t.Context()) == nil {
				t.Fatal("the first read yielded no deadline")
			}

			refuseNodeReads(client, failure)
			assertStuckAt(t, deadline, want)
		})
	}
}

func TestNodeDeadlineIsStickyWhenTheAnnotationStopsBeingReadable(t *testing.T) {
	for name, replacement := range map[string]string{
		"the annotation is removed":       "",
		"the annotation becomes unusable": "tomorrow",
	} {
		t.Run(name, func(t *testing.T) {
			want := armedDeadline()
			deadline, client := nodeDeadlineFor(annotatedNode(provider.FormatExpiry(want)))
			if deadline.read(t.Context()) == nil {
				t.Fatal("the first read yielded no deadline")
			}

			if _, err := client.CoreV1().Nodes().Update(t.Context(), annotatedNode(replacement), metav1.UpdateOptions{}); err != nil {
				t.Fatalf("rewrite the node annotation: %v", err)
			}
			assertStuckAt(t, deadline, want)
		})
	}
}

func TestNodeDeadlineSurvivesAMissingKubeconfig(t *testing.T) {
	deadline := newNodeDeadline(filepath.Join(t.TempDir(), "kubelet.kubeconfig"), testNodeName, nil)

	if got := deadline.read(t.Context()); got != nil {
		t.Errorf("deadline = %s, want none when the kubeconfig is absent", got)
	}
}

func TestNodeDeadlineIsArmedFromTheSeedBeforeAnyNodeReadSucceeds(t *testing.T) {
	seed := armedDeadline()
	deadline, client := seededNodeDeadlineFor(annotatedNode(""), &seed)
	refuseNodeReads(client, errors.New("dial tcp 127.0.0.1:6444: connect: connection refused"))

	assertStuckAt(t, deadline, seed)
}

func TestNodeDeadlineKeepsTheSeedWhileTheNodeIsNeverReachable(t *testing.T) {
	seed := armedDeadline()
	deadline := newNodeDeadline(filepath.Join(t.TempDir(), "kubelet.kubeconfig"), testNodeName, &seed)

	assertStuckAt(t, deadline, seed)
	assertStuckAt(t, deadline, seed)
}

func TestNodeDeadlineReplacesTheSeedWithTheAnnotation(t *testing.T) {
	for name, offset := range map[string]time.Duration{
		"the annotation renews past the seed": time.Hour,
		"the annotation falls short of it":    -30 * time.Minute,
	} {
		t.Run(name, func(t *testing.T) {
			seed := armedDeadline()
			want := seed.Add(offset)
			deadline, _ := seededNodeDeadlineFor(annotatedNode(provider.FormatExpiry(want)), &seed)

			got := deadline.read(t.Context())
			if got == nil {
				t.Fatal("an annotated node yielded no deadline")
			}
			if !got.Equal(want) {
				t.Errorf("deadline = %s, want the annotation %s to replace the seed %s", got, want, seed)
			}
		})
	}
}

func TestNodeDeadlinePreservesTheSeedWhenTheNodeReadFails(t *testing.T) {
	seed := armedDeadline()
	annotation := provider.FormatExpiry(seed.Add(time.Hour))
	deadline, client := seededNodeDeadlineFor(annotatedNode(annotation), &seed)
	refuseNodeReads(client, apierrors.NewForbidden(corev1.Resource("nodes"), testNodeName, errors.New("no permission")))

	assertStuckAt(t, deadline, seed)
	assertStuckAt(t, deadline, seed)
}
