package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
)

func TestFinalizerIsDurableBeforeAnythingBillableExists(t *testing.T) {
	h := newHarness(t)

	res, err := h.reconcile()
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}

	if res.RequeueAfter != stepRequeue {
		t.Errorf("first pass requeued after %s, want %s", res.RequeueAfter, stepRequeue)
	}
	if !h.hasFinalizer() {
		t.Error("first pass did not persist the finalizer")
	}
	if calls := h.prov.CreateCalls(); len(calls) != 0 {
		t.Errorf("provider was called %d times before the finalizer was durable", len(calls))
	}
	if h.lease().Status.AcceptedAt != nil {
		t.Error("the deadline started before the finalizer was durable")
	}
}

func TestAcceptanceStartsTheClockOnceAndOnlyOnce(t *testing.T) {
	h := newHarness(t)
	h.clock.Advance(30 * time.Minute)

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("finalizer pass: %v", err)
	}
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("acceptance pass: %v", err)
	}

	accepted := h.lease().Status.AcceptedAt
	if accepted == nil {
		t.Fatal("acceptance did not record a start time")
	}
	if !accepted.Time.Equal(h.clock.Now()) {
		t.Errorf("acceptance recorded %s, want the current time %s", accepted.Time, h.clock.Now())
	}
	if want := accepted.Add(testLeaseDuration); !h.lease().Status.ExpiresAt.Time.Equal(want) {
		t.Errorf("deadline is %s, want acceptance plus the requested duration %s", h.lease().Status.ExpiresAt.Time, want)
	}

	h.clock.Advance(time.Minute)
	h.settle()

	if got := h.lease().Status.AcceptedAt; !got.Time.Equal(accepted.Time) {
		t.Errorf("acceptance moved to %s on a later pass, want %s", got.Time, accepted.Time)
	}
}

func TestIntentIsRecordedBeforeTheProviderIsCalled(t *testing.T) {
	h := newHarness(t)

	for pass := range 3 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}

	entry := h.instanceStatus(h.instanceName(0))
	if entry.Phase != v1alpha1.InstancePhaseIntended {
		t.Errorf("instance phase is %q, want %q", entry.Phase, v1alpha1.InstancePhaseIntended)
	}
	if calls := h.prov.CreateCalls(); len(calls) != 0 {
		t.Errorf("provider was called %d times before the intent was durable", len(calls))
	}
}

func TestACrashAfterTheIntentStillCreatesExactlyOneInstance(t *testing.T) {
	h := newHarness(t)

	for pass := range 3 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}
	h.settle()

	if got := len(h.prov.CreateCalls()); got != 1 {
		t.Errorf("provider create was called %d times, want 1", got)
	}
	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances, want 1", got)
	}
}

func TestACrashBeforeTheStatusWriteAdoptsTheCreatedInstance(t *testing.T) {
	h := newHarness(t)
	for pass := range 4 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}
	name := h.instanceName(0)
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances before the simulated crash, want 1", got)
	}

	lease := h.lease()
	entry := findInstance(lease, name)
	entry.Phase = v1alpha1.InstancePhaseIntended
	entry.ProviderID = ""
	if err := h.api.Status().Update(t.Context(), lease); err != nil {
		t.Fatalf("rewind the lost status write: %v", err)
	}

	h.settle()

	if got := len(h.prov.CreateCalls()); got != 1 {
		t.Errorf("provider create was called %d times after a lost status write, want 1", got)
	}
	recovered := h.instanceStatus(name)
	if recovered.Phase != v1alpha1.InstancePhaseCreated {
		t.Errorf("instance phase is %q, want %q", recovered.Phase, v1alpha1.InstancePhaseCreated)
	}
	if recovered.ProviderID == "" {
		t.Error("the provider identifier was not recovered by adoption")
	}
}

func TestAnUnrecordedInstanceIsAdoptedByItsLeaseLabel(t *testing.T) {
	h := newHarness(t)
	for pass := range 2 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}

	name := h.instanceName(0)
	h.prov.Seed(provider.Instance{Name: name, Labels: instanceLabels(h.lease())})
	h.settle()

	if got := len(h.prov.CreateCalls()); got != 0 {
		t.Errorf("provider create was called %d times despite an adoptable instance", got)
	}
	adopted := h.instanceStatus(name)
	if adopted.Phase != v1alpha1.InstancePhaseCreated {
		t.Errorf("adopted instance phase is %q, want %q", adopted.Phase, v1alpha1.InstancePhaseCreated)
	}
	if adopted.ProviderID == "" {
		t.Error("adoption did not record the provider identifier")
	}
}

func TestEveryInstanceCarriesItsLeaseAndDeadlineFromTheCreateCall(t *testing.T) {
	h := newHarness(t)
	h.settle()

	calls := h.prov.CreateCalls()
	if len(calls) != 1 {
		t.Fatalf("provider create was called %d times, want 1", len(calls))
	}
	lease := h.lease()
	want := map[string]string{
		provider.ManagedByLabelKey: provider.ManagedByValue,
		provider.PoolLabelKey:      provider.ReservedPoolValue,
		provider.ExpiresAtLabelKey: provider.FormatExpiry(lease.Status.ExpiresAt.Time),
		LeaseNameLabelKey:          lease.Name,
		LeaseUIDLabelKey:           string(lease.UID),
	}
	for key, value := range want {
		if got := calls[0].Labels[key]; got != value {
			t.Errorf("create label %s is %q, want %q", key, got, value)
		}
	}
	if calls[0].Region != testRegion || calls[0].Size != testSize {
		t.Errorf("create requested %q/%q, want %q/%q", calls[0].Region, calls[0].Size, testRegion, testSize)
	}
}

func TestCreateRetriesAfterARetryableFailure(t *testing.T) {
	h := newHarness(t)
	attempts := 0
	h.prov.FailCreate = func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("provider is rate limiting")
		}
		return nil
	}

	for pass := range 3 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("the failing create pass reported success")
	}

	name := h.instanceName(0)
	if h.instanceStatus(name).LastError == "" {
		t.Error("the create failure was not recorded on the instance")
	}

	h.settle()

	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances after the retry, want 1", got)
	}
	if got := h.instanceStatus(name); got.Phase != v1alpha1.InstancePhaseCreated || got.LastError != "" {
		t.Errorf("instance settled as %q with error %q, want %q and no error", got.Phase, got.LastError, v1alpha1.InstancePhaseCreated)
	}
}

func TestACreateThatTimedOutButSucceededIsAdoptedNotDuplicated(t *testing.T) {
	h := newHarness(t)
	for pass := range 2 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}

	labels := instanceLabels(h.lease())
	h.prov.FailCreate = func(name string) error {
		h.prov.FailCreate = nil
		h.prov.Seed(provider.Instance{Name: name, Labels: labels})
		return context.DeadlineExceeded
	}

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("intent pass: %v", err)
	}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("the timed out create pass reported success")
	}

	h.settle()

	if got := len(h.prov.CreateCalls()); got != 1 {
		t.Errorf("provider create was called %d times, want 1", got)
	}
	if got := len(h.providerInstances()); got != 1 {
		t.Errorf("provider holds %d instances, want 1", got)
	}
}

func TestAJoinedNodeIsLabelledTaintedAndCountedReady(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	node, ok := h.node(name)
	if !ok {
		t.Fatalf("node %q disappeared", name)
	}
	if got := node.Labels[LeaseUIDLabelKey]; got != string(h.lease().UID) {
		t.Errorf("node label %s is %q, want the lease uid", LeaseUIDLabelKey, got)
	}
	if !hasBurstTaint(node) {
		t.Errorf("node %q carries no %s taint", name, k8s.BurstTaintKey)
	}
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionTrue)
	if got := h.instanceStatus(name).Phase; got != v1alpha1.InstancePhaseJoined {
		t.Errorf("instance phase is %q, want %q", got, v1alpha1.InstancePhaseJoined)
	}
	if got := h.lease().Status.Phase; got != v1alpha1.LeasePhaseActive {
		t.Errorf("lease phase is %q, want %q", got, v1alpha1.LeasePhaseActive)
	}
}

func TestANodeThatNeverRegistersIsRetiredAndItsSlotIsNotRefilled(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances before the timeout, want 1", got)
	}

	h.clock.Advance(nodeRegistrationTimeout + time.Minute)
	h.settle()

	h.assertProviderEmpty()
	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionFalse)
	retired := h.instanceStatus(name)
	if retired.Phase != v1alpha1.InstancePhaseReleased {
		t.Errorf("retired instance phase is %q, want %q", retired.Phase, v1alpha1.InstancePhaseReleased)
	}
	if retired.LastError == "" {
		t.Error("the registration timeout was not recorded on the instance")
	}
	if got := len(h.prov.CreateCalls()); got != 1 {
		t.Errorf("provider create was called %d times, want 1: a retired slot must not be refilled", got)
	}
}

func TestAnInstanceThatNeverLaunchesIsRetired(t *testing.T) {
	h := newHarness(t)
	h.prov.FailCreate = func(string) error { return errors.New("provider is unreachable") }

	h.settleIgnoringErrors(6)
	h.clock.Advance(instanceLaunchTimeout + time.Minute)
	h.settleIgnoringErrors(6)

	h.assertProviderEmpty()
	h.assertCondition(v1alpha1.ConditionDegraded, metav1.ConditionTrue)
	retired := h.instanceStatus(h.instanceName(0))
	if retired.Phase != v1alpha1.InstancePhaseReleased {
		t.Errorf("retired instance phase is %q, want %q", retired.Phase, v1alpha1.InstancePhaseReleased)
	}
}

func TestAnUnresolvableProviderBlocksAcceptanceAndSpending(t *testing.T) {
	h := newHarness(t)
	h.providerErr = errors.New("credentials secret is missing")

	h.settleIgnoringErrors(3)

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionFalse)
	if h.lease().Status.AcceptedAt != nil {
		t.Error("the deadline started without a usable provider")
	}
	if got := len(h.prov.CreateCalls()); got != 0 {
		t.Errorf("provider create was called %d times without a usable provider", got)
	}
	if !h.hasFinalizer() {
		t.Error("the finalizer was not persisted before the provider was resolved")
	}
}

func TestAProviderThatCannotGuaranteeTeardownBlocksAcceptance(t *testing.T) {
	h := newHarness(t)
	h.dropNodeCredential()

	h.settleIgnoringErrors(3)

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonProviderUnavailable, "nodeCredentialSecretRef")
	if h.lease().Status.AcceptedAt != nil {
		t.Error("the deadline started without a teardown guarantee")
	}
	if got := len(h.prov.CreateCalls()); got != 0 {
		t.Errorf("provider create was called %d times without a teardown guarantee", got)
	}
}

func TestRemovingTheNodeCredentialStillReleasesAnAcceptedLease(t *testing.T) {
	h := newHarness(t)
	h.settle()
	if got := len(h.providerInstances()); got != 1 {
		t.Fatalf("provider holds %d instances before the credential is removed, want 1", got)
	}

	h.dropNodeCredential()
	h.deleteLease()
	h.settle()

	h.assertProviderEmpty()
	if !h.leaseGone() {
		t.Error("the finalizer was retained after a lease whose provider config lost its node credential")
	}
}

func TestTheRequeueNeverOvershootsTheDeadline(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Duration = metav1.Duration{Duration: 5 * time.Minute}
	})
	h.settle()

	remaining := 10 * time.Second
	h.clock.Advance(5*time.Minute - remaining)
	res, err := h.reconcile()
	if err != nil {
		t.Fatalf("reconcile near the deadline: %v", err)
	}

	if res.RequeueAfter <= 0 || res.RequeueAfter > remaining {
		t.Errorf("requeue after %s, want a positive delay no later than the %s left", res.RequeueAfter, remaining)
	}
}

func TestWorkloadMigrationRunsOnceInstancesAreReady(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload()
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionUnknown)

	h.joinNode(h.instanceName(0), true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue)
	if got := h.lease().Status.MigratedWorkloads; len(got) != 1 || got[0] != "deployment/api" {
		t.Errorf("migrated workloads are %v, want [deployment/api]", got)
	}
	if _, ok := h.deploymentAnnotations()[k8s.PrePlacementAnnotationKey]; !ok {
		t.Error("the deployment carries no saved placement annotation")
	}
}
