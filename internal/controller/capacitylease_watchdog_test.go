package controller

import (
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	testRenewInterval = time.Minute
	testSlack         = 2 * time.Minute
)

var testInstant = time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)

func testPolicy(renew, slack time.Duration) v1alpha1.WatchdogPolicy {
	return v1alpha1.WatchdogPolicy{
		RenewInterval: metav1.Duration{Duration: renew},
		Slack:         metav1.Duration{Duration: slack},
		MaxLifetime:   metav1.Duration{Duration: time.Hour},
	}
}

func leaseExpiringIn(remaining time.Duration) *v1alpha1.CapacityLease {
	return &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", UID: "burst-uid"},
		Status: v1alpha1.CapacityLeaseStatus{
			ExpiresAt: &metav1.Time{Time: testInstant.Add(remaining)},
		},
	}
}

func adoptedNode(name string, lease *v1alpha1.CapacityLease, annotations map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      nodeLabels(lease),
			Annotations: annotations,
		},
	}
}

func reconcilerWith(nodes ...runtime.Object) (*CapacityLeaseReconciler, *k8sfake.Clientset) {
	kube := newKubeClient(nodes...)
	return &CapacityLeaseReconciler{Kube: kube, Clock: func() time.Time { return testInstant }}, kube
}

func countActions(kube *k8sfake.Clientset, verb string) int {
	count := 0
	for _, action := range kube.Actions() {
		if action.GetVerb() == verb {
			count++
		}
	}
	return count
}

func TestWatchdogDeadlineNeverOutlivesTheLease(t *testing.T) {
	policy := testPolicy(testRenewInterval, testSlack)

	tests := map[string]struct {
		remaining time.Duration
		want      time.Duration
	}{
		"a lease with room to spare gets one renew interval plus slack": {remaining: time.Hour, want: 3 * time.Minute},
		"a lease ending sooner clamps the deadline to its own expiry":   {remaining: 90 * time.Second, want: 90 * time.Second},
		"a lease ending on the boundary keeps the shorter deadline":     {remaining: 3 * time.Minute, want: 3 * time.Minute},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := watchdogDeadline(leaseExpiringIn(tc.remaining), policy, testInstant)
			if want := testInstant.Add(tc.want); !got.Equal(want) {
				t.Errorf("deadline = %s, want %s", got, want)
			}
		})
	}
}

func TestWatchdogDeadlineIsWholeSecondsSoTheAnnotationRoundTrips(t *testing.T) {
	now := testInstant.Add(1234 * time.Millisecond)

	deadline := watchdogDeadline(leaseExpiringIn(time.Hour), testPolicy(testRenewInterval, testSlack), now)

	parsed, readable := provider.ParseExpiryValue(provider.FormatExpiry(deadline))
	if !readable {
		t.Fatal("the deadline did not survive being formatted")
	}
	if !parsed.Equal(deadline) {
		t.Errorf("round tripped deadline = %s, want %s", parsed, deadline)
	}
}

func TestShouldRenewFiresAboutOncePerRenewInterval(t *testing.T) {
	policy := testPolicy(testRenewInterval, testSlack)
	deadline := testInstant.Add(testRenewInterval + testSlack)
	annotated := provider.FormatExpiry(deadline)

	tests := map[string]struct {
		annotated string
		deadline  time.Time
		elapsed   time.Duration
		want      bool
	}{
		"an unannotated node is renewed at once":      {annotated: "", deadline: deadline, want: true},
		"an unreadable annotation is renewed at once": {annotated: "tomorrow", deadline: deadline, want: true},
		"a fresh deadline is left alone":              {annotated: annotated, deadline: deadline, want: false},
		"a deadline is left alone until slack is all it has": {
			annotated: annotated, deadline: deadline, elapsed: testRenewInterval - time.Second, want: false,
		},
		"a deadline is renewed once only slack remains": {
			annotated: annotated, deadline: deadline, elapsed: testRenewInterval, want: true,
		},
		"a shorter deadline is written immediately": {
			annotated: provider.FormatExpiry(testInstant.Add(time.Hour)), deadline: deadline, want: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := shouldRenew(tc.annotated, tc.deadline, policy, testInstant.Add(tc.elapsed))
			if got != tc.want {
				t.Errorf("shouldRenew = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPatchNodeMarksWritesAnAnnotationOnlyDifference(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	node := adoptedNode("burst-0", lease, nil)
	r, kube := reconcilerWith(node)

	deadline := provider.FormatExpiry(testInstant.Add(3 * time.Minute))
	annotations := map[string]string{provider.WatchdogDeadlineAnnotationKey: deadline}
	if err := r.patchNodeMarks(t.Context(), lease, node, annotations); err != nil {
		t.Fatalf("patch node marks: %v", err)
	}

	if got := countActions(kube, "patch"); got != 1 {
		t.Fatalf("the node was patched %d times, want 1: a renewal must not be mistaken for a clean node", got)
	}
	stored, err := kube.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read the node back: %v", err)
	}
	if got := stored.Annotations[provider.WatchdogDeadlineAnnotationKey]; got != deadline {
		t.Errorf("node annotation = %q, want %q", got, deadline)
	}
}

func TestPatchNodeMarksSkipsANodeThatAlreadyCarriesThem(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	deadline := provider.FormatExpiry(testInstant.Add(3 * time.Minute))
	annotations := map[string]string{provider.WatchdogDeadlineAnnotationKey: deadline}
	node := adoptedNode("burst-0", lease, annotations)
	r, kube := reconcilerWith(node)

	if err := r.patchNodeMarks(t.Context(), lease, node, annotations); err != nil {
		t.Fatalf("patch node marks: %v", err)
	}

	if got := countActions(kube, "patch"); got != 0 {
		t.Errorf("the node was patched %d times, want none", got)
	}
}

func TestPatchNodeMarksLeavesTaintsAndForeignMetadataAlone(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	node := adoptedNode("burst-0", lease, map[string]string{"example.com/owner": "someone"})
	node.Labels["example.com/rack"] = "b12"
	node.Spec.Taints = []corev1.Taint{{Key: "example.com/hold", Effect: corev1.TaintEffectNoExecute}}
	r, kube := reconcilerWith(node)

	annotations := map[string]string{provider.WatchdogDeadlineAnnotationKey: provider.FormatExpiry(testInstant)}
	if err := r.patchNodeMarks(t.Context(), lease, node, annotations); err != nil {
		t.Fatalf("patch node marks: %v", err)
	}

	stored, err := kube.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read the node back: %v", err)
	}
	if len(stored.Spec.Taints) != 1 || stored.Spec.Taints[0].Key != "example.com/hold" {
		t.Errorf("node taints = %v, want the existing taint untouched", stored.Spec.Taints)
	}
	if got := stored.Labels["example.com/rack"]; got != "b12" {
		t.Errorf("foreign label = %q, want it preserved", got)
	}
	if got := stored.Annotations["example.com/owner"]; got != "someone" {
		t.Errorf("foreign annotation = %q, want it preserved", got)
	}
}

func TestEnsureBurstTaintRetriesAConflictedUpdate(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	node := adoptedNode("burst-0", lease, nil)
	r, kube := reconcilerWith(node)

	conflicted := false
	kube.PrependReactor("update", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicted {
			return false, nil, nil
		}
		conflicted = true
		return true, nil, apierrors.NewConflict(corev1.Resource("nodes"), node.Name, errors.New("the node was modified"))
	})

	if err := r.ensureBurstTaint(t.Context(), lease, node); err != nil {
		t.Fatalf("ensure burst taint: %v", err)
	}

	stored, err := kube.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read the node back: %v", err)
	}
	if !hasBurstTaint(stored, lease.Name) {
		t.Errorf("node carries no %s taint after the retry", k8s.BurstTaintKey)
	}
	if !conflicted {
		t.Error("the conflict reactor never fired")
	}
}

func TestEnsureBurstTaintKeepsTaintsItDoesNotOwn(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	node := adoptedNode("burst-0", lease, nil)
	node.Spec.Taints = []corev1.Taint{{Key: "example.com/hold", Effect: corev1.TaintEffectNoExecute}}
	r, kube := reconcilerWith(node)

	if err := r.ensureBurstTaint(t.Context(), lease, node); err != nil {
		t.Fatalf("ensure burst taint: %v", err)
	}

	stored, err := kube.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read the node back: %v", err)
	}
	if len(stored.Spec.Taints) != 2 || !hasBurstTaint(stored, lease.Name) {
		t.Errorf("node taints = %v, want the existing taint plus the burst taint", stored.Spec.Taints)
	}
}

func TestEnsureBurstTaintSkipsANodeThatCarriesIt(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	node := adoptedNode("burst-0", lease, nil)
	node.Spec.Taints = []corev1.Taint{burstTaint(lease)}
	r, kube := reconcilerWith(node)

	if err := r.ensureBurstTaint(t.Context(), lease, node); err != nil {
		t.Fatalf("ensure burst taint: %v", err)
	}

	if got := countActions(kube, "update"); got != 0 {
		t.Errorf("the node was updated %d times, want none", got)
	}
}

func TestNextPollFollowsTheShortestDeadlineInPlay(t *testing.T) {
	tests := map[string]struct {
		renew     time.Duration
		remaining time.Duration
		want      time.Duration
	}{
		"a renew interval shorter than the poll wins":   {renew: 10 * time.Second, remaining: time.Hour, want: 10 * time.Second},
		"a renew interval longer than the poll is kept": {renew: 5 * time.Minute, remaining: time.Hour, want: DefaultPollInterval},
		"an imminent expiry beats both":                 {renew: 10 * time.Second, remaining: 4 * time.Second, want: 4 * time.Second},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r, _ := reconcilerWith()
			got := r.nextPoll(leaseExpiringIn(tc.remaining), testPolicy(tc.renew, testSlack)).RequeueAfter
			if got != tc.want {
				t.Errorf("requeue after = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTheConfiguredPollIntervalIsHonouredAndZeroMeansTheDefault(t *testing.T) {
	tests := map[string]struct {
		configured time.Duration
		want       time.Duration
	}{
		"zero falls back to the default": {configured: 0, want: DefaultPollInterval},
		"a configured interval wins":     {configured: 5 * time.Second, want: 5 * time.Second},
		"a longer interval also wins":    {configured: 2 * time.Minute, want: 2 * time.Minute},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r, _ := reconcilerWith()
			r.PollInterval = tc.configured
			got := r.nextPoll(leaseExpiringIn(time.Hour), testPolicy(time.Hour, testSlack)).RequeueAfter
			if got != tc.want {
				t.Errorf("requeue after = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRecordWatchdogDeadlineOnlyReportsRealChanges(t *testing.T) {
	lease := leaseExpiringIn(time.Hour)
	deadline := testInstant.Add(3 * time.Minute)

	if !recordWatchdogDeadline(lease, deadline) {
		t.Fatal("the first deadline was not recorded")
	}
	if lease.Status.WatchdogDeadline == nil || !lease.Status.WatchdogDeadline.Time.Equal(deadline) {
		t.Fatalf("status deadline = %v, want %s", lease.Status.WatchdogDeadline, deadline)
	}
	if recordWatchdogDeadline(lease, deadline) {
		t.Error("an unchanged deadline was reported as a status change")
	}
}

func TestAnAdoptedNodeCarriesADeadlineThatMatchesTheLeaseStatus(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	annotated := h.nodeDeadline(name)
	status := h.lease().Status.WatchdogDeadline
	if status == nil {
		t.Fatal("the lease records no watchdog deadline")
	}
	if !status.Time.Equal(annotated) {
		t.Errorf("status deadline = %s, want the annotated %s", status.Time, annotated)
	}
	if want := h.clock.Now().Add(testRenewInterval + testSlack); !annotated.Equal(want.UTC().Truncate(time.Second)) {
		t.Errorf("annotated deadline = %s, want %s", annotated, want)
	}
}

func TestTheNodeDeadlineIsRenewedOncePerRenewInterval(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	armed := h.nodeDeadline(name)

	h.clock.Advance(testRenewInterval / 2)
	h.settle()
	if got := h.nodeDeadline(name); !got.Equal(armed) {
		t.Errorf("deadline = %s after half a renew interval, want the armed %s", got, armed)
	}

	h.clock.Advance(testRenewInterval)
	h.settle()
	renewed := h.nodeDeadline(name)
	if !renewed.After(armed) {
		t.Errorf("deadline = %s after a full renew interval, want it past the armed %s", renewed, armed)
	}
	if status := h.lease().Status.WatchdogDeadline; status == nil || !status.Time.Equal(renewed) {
		t.Errorf("status deadline = %v, want the renewed %s", status, renewed)
	}
}

func TestTheNodeDeadlineIsClampedToTheLeaseExpiry(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Duration = metav1.Duration{Duration: 5 * time.Minute}
	})
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.clock.Advance(4 * time.Minute)
	h.settle()

	expiry := h.lease().Status.ExpiresAt.Time
	if got := h.nodeDeadline(name); !got.Equal(expiry.UTC().Truncate(time.Second)) {
		t.Errorf("deadline = %s, want it clamped to the lease expiry %s", got, expiry)
	}
}

func (h *harness) nodeDeadline(name string) time.Time {
	h.t.Helper()
	node, ok := h.node(name)
	if !ok {
		h.t.Fatalf("node %q disappeared", name)
	}
	raw, annotated := node.Annotations[provider.WatchdogDeadlineAnnotationKey]
	if !annotated {
		h.t.Fatalf("node %q carries no %s annotation", name, provider.WatchdogDeadlineAnnotationKey)
	}
	deadline, readable := provider.ParseExpiryValue(raw)
	if !readable {
		h.t.Fatalf("node %q carries the unreadable deadline %q", name, raw)
	}
	return deadline
}
