package controller

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func TestAStatusWriteConvergesWhenTheLeaseChangedUnderneathIt(t *testing.T) {
	h := newHarness(t)
	racer := &staleWriteRacer{Client: h.api, key: client.ObjectKey{Name: h.name}}
	h.wrapAPI = func(client.Client) client.Client { return racer }

	h.settle()

	if racer.raceErr != nil {
		t.Fatalf("provoke a concurrent write: %v", racer.raceErr)
	}
	if racer.conflicts != 1 {
		t.Fatalf("the status write saw %d conflicts, want exactly one to be exercised", racer.conflicts)
	}

	lease := h.lease()
	if lease.Status.AcceptedAt == nil {
		t.Error("the conflicting pass lost the acceptance it was writing")
	}
	if got := lease.Annotations[raceAnnotationKey]; got != "1" {
		t.Errorf("the concurrent annotation is %q, want the retry to keep it", got)
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
	if !hasBurstTaint(node, h.lease().Name) {
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

func TestARepeatedMigrationPassReportsTheSameWorkloads(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload()
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.settle()

	if _, err := h.reconciler().migrateWorkload(t.Context(), h.lease(), testWorkloadNS); err != nil {
		t.Fatalf("second migration pass: %v", err)
	}

	if got := h.lease().Status.MigratedWorkloads; len(got) != 1 || got[0] != "deployment/api" {
		t.Errorf("migrated workloads are %v after a second pass, want [deployment/api]", got)
	}
	h.assertConditionDetail(v1alpha1.ConditionWorkloadMigrated, reasonMigrated, "1 workloads moved onto burst capacity")
}

func TestAWorkloadNamespaceWithoutWorkloadsReportsNone(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.settle()

	h.assertConditionDetail(v1alpha1.ConditionWorkloadMigrated, reasonMigrated, "0 workloads moved onto burst capacity")
	if got := h.lease().Status.MigratedWorkloads; len(got) != 0 {
		t.Errorf("the lease lists migrated workloads %v for a namespace that holds none", got)
	}
}

func TestWatchdogArmedIsTrueWhenEveryJoinedNodeIsFresh(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.armNode(name, h.clock.Now())
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue)
	if events := h.events(); len(events) != 0 {
		t.Errorf("an armed node recorded %d events, want none", len(events))
	}
}

func TestWatchdogArmedIsFalseWhenAJoinedNodeNeverArmed(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionWatchdogArmed, reasonWatchdogUnarmed, name)

	events := h.events()
	if len(events) != 1 {
		t.Fatalf("recorded %d events on the false transition, want 1", len(events))
	}
	if !strings.Contains(events[0], reasonWatchdogUnarmed) {
		t.Errorf("event %q does not mention reason %q", events[0], reasonWatchdogUnarmed)
	}
}

func TestWatchdogArmedDoesNotReemitTheEventWhileStillFalse(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	if events := h.events(); len(events) != 1 {
		t.Fatalf("recorded %d events on the first false transition, want 1", len(events))
	}

	h.settle()
	if events := h.events(); len(events) != 0 {
		t.Errorf("staying false recorded %d more events, want none", len(events))
	}
}

func TestWatchdogArmedIsFalseWhenTheAnnotationIsStaleBeyondTheWindow(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.armNode(name, h.clock.Now())
	h.settle()
	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue)

	h.clock.Advance(watchdogArmedStalenessWindow(testPolicy(testRenewInterval, testSlack)))
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse)
}

func TestWatchdogArmedRecoversToTrueOnceRefreshed(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()
	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse)
	h.events()

	h.armNode(name, h.clock.Now())
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue)
	if events := h.events(); len(events) != 0 {
		t.Errorf("recovering to true recorded %d events, want none", len(events))
	}
}

func TestWatchdogArmedRequiresEveryJoinedInstanceToBeFresh(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Replicas = 2
	})
	h.settle()

	armedName := h.instanceName(0)
	unarmedName := h.instanceName(1)
	h.joinNode(armedName, true)
	h.joinNode(unarmedName, true)
	h.armNode(armedName, h.clock.Now())
	h.settle()

	h.assertCondition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse)
	current := h.condition(v1alpha1.ConditionWatchdogArmed)
	if !strings.Contains(current.Message, unarmedName) {
		t.Errorf("message %q does not name the unarmed instance %q", current.Message, unarmedName)
	}
	if strings.Contains(current.Message, armedName) {
		t.Errorf("message %q unexpectedly names the armed instance %q", current.Message, armedName)
	}
}

func TestInstanceTypeIsSetAtAcceptanceFromSpecSize(t *testing.T) {
	h := newHarness(t)

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("finalizer pass: %v", err)
	}
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("acceptance pass: %v", err)
	}

	lease := h.lease()
	if lease.Status.AcceptedAt == nil {
		t.Fatal("acceptance did not record a start time")
	}
	if got := lease.Status.InstanceType; got != testSize {
		t.Errorf("instanceType is %q, want %q", got, testSize)
	}
}

func TestAcceptanceReplayLeavesAnAlreadyLatchedInstanceTypeAlone(t *testing.T) {
	h := newHarness(t)
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("finalizer pass: %v", err)
	}
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("acceptance pass: %v", err)
	}
	if latched := h.lease().Status.InstanceType; latched != testSize {
		t.Fatalf("instanceType is %q at acceptance, want %q", latched, testSize)
	}

	lease := h.lease()
	lease.Status.InstanceType = testLargeSize
	lease.Status.AcceptedAt = nil
	lease.Status.ExpiresAt = nil
	if err := h.api.Status().Update(h.t.Context(), lease); err != nil {
		t.Fatalf("rewind acceptance to simulate a lost write: %v", err)
	}

	if _, err := h.reconcile(); err != nil {
		t.Fatalf("replayed acceptance pass: %v", err)
	}

	if got := h.lease().Status.InstanceType; got != testLargeSize {
		t.Errorf("instanceType is %q after acceptance replayed, want the latched %q", got, testLargeSize)
	}
	h.assertCounter(instanceTypeSelectedMetric,
		map[string]string{"instance_type": testSize, "strategy": "pinned"}, 1)
	h.assertCounter(instanceTypeSelectedMetric,
		map[string]string{"instance_type": testLargeSize, "strategy": "pinned"}, 0)
}

func TestReadyAtIsSetWhenInstancesFirstBecomeReady(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionTrue)
	readyAt := h.lease().Status.ReadyAt
	if readyAt == nil {
		t.Fatal("readyAt was not set when instances first became ready")
	}
	if !readyAt.Time.Equal(h.clock.Now()) {
		t.Errorf("readyAt is %s, want the current time %s", readyAt.Time, h.clock.Now())
	}
}

func TestReadyAtIsNotRewrittenWhenANodeFlaps(t *testing.T) {
	h := newHarness(t)
	h.settle()

	name := h.instanceName(0)
	h.joinNode(name, true)
	h.settle()

	first := h.lease().Status.ReadyAt
	if first == nil {
		t.Fatal("readyAt was not set on the first ready pass")
	}

	h.clock.Advance(time.Minute)
	h.setNodeReady(name, false)
	h.settle()
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionFalse)

	h.clock.Advance(time.Minute)
	h.setNodeReady(name, true)
	h.settle()
	h.assertCondition(v1alpha1.ConditionInstancesReady, metav1.ConditionTrue)

	if got := h.lease().Status.ReadyAt; !got.Time.Equal(first.Time) {
		t.Errorf("readyAt moved to %s after a flap, want it to stay at %s", got.Time, first.Time)
	}
}

func TestCreateInstanceRecordsTheProvidersCreationTimestamp(t *testing.T) {
	h := newHarness(t)

	for pass := range 3 {
		if _, err := h.reconcile(); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}
	intended := h.instanceStatus(h.instanceName(0)).CreatedAt
	if intended == nil {
		t.Fatal("the recorded intent carries no created time")
	}

	h.clock.Advance(time.Minute)
	if _, err := h.reconcile(); err != nil {
		t.Fatalf("create pass: %v", err)
	}

	if calls := h.prov.CreateCalls(); len(calls) != 1 {
		t.Fatalf("provider create was called %d times, want 1", len(calls))
	}
	created := h.instanceStatus(h.instanceName(0)).CreatedAt
	if created == nil {
		t.Fatal("the created instance carries no created time")
	}
	if created.Time.Equal(intended.Time) {
		t.Error("createdAt still reflects the intent time, want the provider's create time")
	}
	if !created.Time.Equal(h.clock.Now()) {
		t.Errorf("createdAt is %s, want the provider's create time %s", created.Time, h.clock.Now())
	}
}

func TestASeamlessMigrationRecordsNoWarnings(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload()
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadMigratable, metav1.ConditionTrue)
	if got := h.lease().Status.MigrationWarnings; len(got) != 0 {
		t.Errorf("migration warnings are %+v, want none for a rolling deployment", got)
	}
}

func TestADisruptiveWorkloadIsNamedBeforeItIsMoved(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload(func(deployment *appsv1.Deployment) {
		deployment.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		deployment.Spec.Template.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
	})
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue)
	h.assertConditionDetail(v1alpha1.ConditionWorkloadMigratable, reasonDisruptiveMigration, "1 of 1 workloads")

	warnings := h.lease().Status.MigrationWarnings
	if len(warnings) != 1 || warnings[0].Workload != "deployment/api" {
		t.Fatalf("migration warnings are %+v, want one for deployment/api", warnings)
	}
	want := []string{k8s.ReasonRecreateStrategy, k8s.ReasonNodeSelectorPinned}
	if !reflect.DeepEqual(warnings[0].Reasons, want) {
		t.Errorf("warning reasons are %v, want %v", warnings[0].Reasons, want)
	}
	if got := h.lease().Status.MigratedWorkloads; len(got) != 1 || got[0] != "deployment/api" {
		t.Errorf("migrated workloads are %v, want [deployment/api]: a warning must not block the move", got)
	}
}

func TestAClassificationFailureWarnsWithoutBlockingTheMigration(t *testing.T) {
	h := newHarness(t, func(lease *v1alpha1.CapacityLease) {
		lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespace: testWorkloadNS}
	})
	h.seedWorkload(func(deployment *appsv1.Deployment) {
		deployment.Annotations = map[string]string{k8s.PrePlacementAnnotationKey: "{not json"}
	})
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.settle()

	h.assertCondition(v1alpha1.ConditionWorkloadMigrated, metav1.ConditionTrue)
	h.assertCondition(v1alpha1.ConditionWorkloadMigratable, metav1.ConditionUnknown)
	h.assertConditionDetail(v1alpha1.ConditionWorkloadMigratable, reasonClassificationFailed, "unmarshal placement")

	if got := h.lease().Status.MigratedWorkloads; len(got) != 1 || got[0] != "deployment/api" {
		t.Errorf("migrated workloads are %v, want [deployment/api]: classification must never block the move", got)
	}
	if got := h.lease().Status.MigrationWarnings; len(got) != 0 {
		t.Errorf("migration warnings are %+v, want none while the classification is unknown", got)
	}
}
