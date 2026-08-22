package manager

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/metrics"
)

const (
	leasesMetric          = "horizon_leases"
	leaseConditionMetric  = "horizon_lease_status_condition"
	unreadableCacheBudget = 50 * time.Millisecond

	seriesWaitTimeout = 10 * time.Second
	seriesPoll        = 20 * time.Millisecond

	testPollInterval = 97 * time.Second
)

var errCacheUnreadable = errors.New("stub: the lease cache cannot be read")

type stubLeaseReader struct {
	client.Reader
	leases []v1alpha1.CapacityLease
	err    error
	block  bool
}

func (s stubLeaseReader) List(ctx context.Context, out client.ObjectList, _ ...client.ListOption) error {
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if s.err != nil {
		return s.err
	}
	list, ok := out.(*v1alpha1.CapacityLeaseList)
	if !ok {
		return errors.New("stub: asked for something other than capacity leases")
	}
	list.Items = s.leases
	return nil
}

func leaseIn(phase v1alpha1.LeasePhase, conditions ...metav1.Condition) v1alpha1.CapacityLease {
	return v1alpha1.CapacityLease{Status: v1alpha1.CapacityLeaseStatus{Phase: phase, Conditions: conditions}}
}

func reporting(condition string, status metav1.ConditionStatus) metav1.Condition {
	return metav1.Condition{Type: condition, Status: status, Reason: "Test"}
}

var wiredReconcilers *reconcilers

// controller-runtime refuses a second manager naming the same controllers, so one serves every run of this suite
var wiredManager = sync.OnceValues(func() (ctrl.Manager, error) {
	if err := os.Setenv(namespaceVar, testNamespace); err != nil {
		return nil, err
	}
	mgr, parts, err := newManager(testEnv.Config,
		Options{MetricsAddress: "0", HealthAddress: "0", PollInterval: testPollInterval})
	if err != nil {
		return nil, err
	}
	wiredReconcilers = parts

	ctx, cancel := context.WithCancel(context.Background())
	stopWiredManager = cancel
	go func() { _ = mgr.GetCache().Start(ctx) }()
	return mgr, nil
})

var stopWiredManager = func() {}

func gatheredSeries(t *testing.T, name string) []*dto.Metric {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather the metric registry: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.GetMetric()
		}
	}
	return nil
}

func seriesValue(t *testing.T, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	for _, series := range gatheredSeries(t, name) {
		matched := 0
		for _, label := range series.GetLabel() {
			if labels[label.GetName()] == label.GetValue() {
				matched++
			}
		}
		if matched == len(labels) {
			return series.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func waitForSeries(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	deadline := time.Now().Add(seriesWaitTimeout)
	for time.Now().Before(deadline) {
		if value, found := seriesValue(t, name, labels); found {
			return value
		}
		time.Sleep(seriesPoll)
	}
	t.Fatalf("%s%v never appeared", name, labels)
	return 0
}

func TestTheLeaseStateSourceCountsPhasesAndDeclaredConditions(t *testing.T) {
	source := leaseStateSource(stubLeaseReader{leases: []v1alpha1.CapacityLease{
		leaseIn(v1alpha1.LeasePhaseActive, reporting(v1alpha1.ConditionAccepted, metav1.ConditionTrue)),
		leaseIn(v1alpha1.LeasePhaseActive, reporting(v1alpha1.ConditionAccepted, metav1.ConditionTrue)),
		leaseIn(v1alpha1.LeasePhaseDegraded,
			reporting(v1alpha1.ConditionAccepted, metav1.ConditionTrue),
			reporting(v1alpha1.ConditionDegraded, metav1.ConditionTrue)),
	}}, leaseStateReadTimeout)

	state := source()

	if got := state.Phases[v1alpha1.LeasePhaseActive]; got != 2 {
		t.Errorf("the active phase counts %d leases, want 2", got)
	}
	if got := state.Phases[v1alpha1.LeasePhaseDegraded]; got != 1 {
		t.Errorf("the degraded phase counts %d leases, want 1", got)
	}
	accepted := metrics.LeaseCondition{Type: v1alpha1.ConditionAccepted, Status: metav1.ConditionTrue}
	if got := state.Conditions[accepted]; got != 3 {
		t.Errorf("%d leases report Accepted=True, want 3", got)
	}
	degraded := metrics.LeaseCondition{Type: v1alpha1.ConditionDegraded, Status: metav1.ConditionTrue}
	if got := state.Conditions[degraded]; got != 1 {
		t.Errorf("%d leases report Degraded=True, want 1", got)
	}
}

func TestTheLeaseStateSourceKeepsUndeclaredPhasesAndConditionsOutOfEveryLabel(t *testing.T) {
	const foreign = "a-phase-and-condition-horizon-never-declared"

	source := leaseStateSource(stubLeaseReader{leases: []v1alpha1.CapacityLease{
		leaseIn(v1alpha1.LeasePhase(foreign), reporting(foreign, metav1.ConditionTrue)),
	}}, leaseStateReadTimeout)

	state := source()

	for phase := range state.Phases {
		if string(phase) == foreign {
			t.Errorf("an undeclared phase reached a label as %q", phase)
		}
	}
	if got := state.Phases[unclassifiedPhase]; got != 1 {
		t.Errorf("the unclassified phase counts %d leases, want 1", got)
	}
	for condition := range state.Conditions {
		if condition.Type == foreign {
			t.Errorf("an undeclared condition reached a label as %q", condition.Type)
		}
	}
	if len(state.Conditions) != 0 {
		t.Errorf("an undeclared condition was counted: %v", state.Conditions)
	}
}

func TestTheLeaseStateSourceHandsOutFreshMapsOnEveryCall(t *testing.T) {
	source := leaseStateSource(stubLeaseReader{leases: []v1alpha1.CapacityLease{
		leaseIn(v1alpha1.LeasePhaseActive, reporting(v1alpha1.ConditionAccepted, metav1.ConditionTrue)),
	}}, leaseStateReadTimeout)

	first := source()
	second := source()
	first.Phases[v1alpha1.LeasePhaseActive] = 99
	first.Conditions[metrics.LeaseCondition{Type: v1alpha1.ConditionAccepted, Status: metav1.ConditionTrue}] = 99

	if got := second.Phases[v1alpha1.LeasePhaseActive]; got != 1 {
		t.Errorf("a later read saw %d active leases, want 1: the source shares its maps", got)
	}
	accepted := metrics.LeaseCondition{Type: v1alpha1.ConditionAccepted, Status: metav1.ConditionTrue}
	if got := second.Conditions[accepted]; got != 1 {
		t.Errorf("a later read saw %d accepted leases, want 1: the source shares its maps", got)
	}
}

func assertNothingCounted(t *testing.T, state metrics.LeaseState, when string) {
	t.Helper()
	if len(state.Phases) != 0 {
		t.Errorf("%s reported %d phases, want none", when, len(state.Phases))
	}
	if len(state.Conditions) != 0 {
		t.Errorf("%s reported %d conditions, want none", when, len(state.Conditions))
	}
}

func TestAnUnreadableLeaseCacheCountsNothing(t *testing.T) {
	source := leaseStateSource(stubLeaseReader{err: errCacheUnreadable}, leaseStateReadTimeout)

	assertNothingCounted(t, source(), "a read of an unreadable cache")
}

func TestALeaseStateReadGivesUpWithinItsBudget(t *testing.T) {
	source := leaseStateSource(stubLeaseReader{block: true}, unreadableCacheBudget)

	done := make(chan metrics.LeaseState, 1)
	go func() { done <- source() }()

	select {
	case state := <-done:
		assertNothingCounted(t, state, "a read that gave up")
	case <-time.After(20 * unreadableCacheBudget):
		t.Fatal("a lease state read outlived its budget and would stall every scrape")
	}
}

func TestACacheThatHasNotStartedCountsNothing(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	unstarted, err := cache.New(testEnv.Config, cache.Options{Scheme: Scheme()})
	if err != nil {
		t.Fatalf("build an unstarted cache: %v", err)
	}
	source := leaseStateSource(unstarted, leaseStateReadTimeout)

	assertNothingCounted(t, source(), "a read before the cache started")
}

func TestTheManagerServesTheLeaseStateGaugesFromItsCache(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	lease := &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: "lease-state-gauges"},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: "hetzner",
			Region:      "nbg1",
			Size:        "cx22",
			Replicas:    1,
			Duration:    metav1.Duration{Duration: time.Hour},
		},
	}
	if err := testEnv.Client.Create(t.Context(), lease); err != nil {
		t.Fatalf("create lease: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Client.Delete(context.Background(), lease) })

	lease.Status.Phase = v1alpha1.LeasePhaseActive
	lease.Status.Conditions = []metav1.Condition{{
		Type:               v1alpha1.ConditionInstancesReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Test",
		LastTransitionTime: metav1.Now(),
	}}
	if err := testEnv.Client.Status().Update(t.Context(), lease); err != nil {
		t.Fatalf("stamp the lease status: %v", err)
	}

	if _, err := wiredManager(); err != nil {
		t.Fatalf("wire the manager: %v", err)
	}

	if got := waitForSeries(t, leasesMetric, map[string]string{"phase": "Active"}); got != 1 {
		t.Errorf("horizon_leases{phase=\"Active\"} is %v, want 1", got)
	}
	ready := map[string]string{"condition": v1alpha1.ConditionInstancesReady, "status": "True"}
	if got := waitForSeries(t, leaseConditionMetric, ready); got != 1 {
		t.Errorf("horizon_lease_status_condition for a ready lease is %v, want 1", got)
	}
}
