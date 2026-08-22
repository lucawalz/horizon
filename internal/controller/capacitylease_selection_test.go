package controller

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

func requiringCapacity(mutators ...func(*v1alpha1.SizeRequirements)) func(*v1alpha1.CapacityLease) {
	return func(lease *v1alpha1.CapacityLease) { requiring(mutators...)(&lease.Spec) }
}

func offering(types ...provider.InstanceType) stubCatalogue {
	return stubCatalogue{types: types}
}

// dyadic rationals, so the recorded rate and the margin over the runner-up are both exact in binary
const (
	chosenRate   = 0.00390625
	runnerUpRate = 0.03125
)

func onARM(it *provider.InstanceType) {
	it.Architecture = string(v1alpha1.ArchitectureARM)
}

func (h *harness) assertLatchedType(want string) {
	h.t.Helper()
	if got := h.lease().Status.InstanceType; got != want {
		h.t.Errorf("instanceType is %q, want %q", got, want)
	}
}

func (h *harness) selection() *v1alpha1.SelectionStatus {
	h.t.Helper()
	recorded := h.lease().Status.Selection
	if recorded == nil {
		h.t.Fatal("the lease records no selection")
	}
	return recorded
}

func (h *harness) selectionEvents() []string {
	h.t.Helper()
	var announced []string
	for _, event := range h.events() {
		if strings.Contains(event, reasonInstanceTypeSelected) {
			announced = append(announced, event)
		}
	}
	return announced
}

func (h *harness) assertAnnouncedSelection(want string) {
	h.t.Helper()
	announced := h.selectionEvents()
	if len(announced) != 1 {
		h.t.Fatalf("the lease announced %d selections, want exactly one: %v", len(announced), announced)
	}
	if announced[0] != want {
		h.t.Errorf("the lease announced %q, want %q", announced[0], want)
	}
}

func (h *harness) assertAnnouncedNothing() {
	h.t.Helper()
	if announced := h.selectionEvents(); len(announced) != 0 {
		h.t.Errorf("the lease announced %v, want no selection", announced)
	}
}

func (h *harness) assertMachineSizes(want string) {
	h.t.Helper()
	instances := h.providerInstances()
	if len(instances) == 0 {
		h.t.Fatal("the provider holds no instance to size")
	}
	for _, inst := range instances {
		if inst.Size != want {
			h.t.Errorf("instance %q was created as %q, want %q", inst.Name, inst.Size, want)
		}
	}
}

func TestLowestPriceSizesALeaseFromItsRequirements(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = offering(candidate(testLargeSize, 8, 16, 0.06), candidate(testSize, 2, 4, 0.02))

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused a lease its requirements can be met: %v", err)
	}

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionTrue)
	h.assertLatchedType(testSize)
	h.assertCounter(instanceTypeSelectedMetric,
		map[string]string{"instance_type": testSize, "strategy": "lowest-price"}, 1)
}

func TestLowestPricePerCoreSizesTheSameCatalogueDifferently(t *testing.T) {
	h := newHarness(t, requiringCapacity(func(r *v1alpha1.SizeRequirements) {
		r.Strategy = v1alpha1.StrategyLowestPricePerCore
	}))
	h.catalogue = offering(candidate(testLargeSize, 8, 16, 0.06), candidate(testSize, 2, 4, 0.02))

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused a lease its requirements can be met: %v", err)
	}

	h.assertLatchedType(testLargeSize)
	h.assertCounter(instanceTypeSelectedMetric,
		map[string]string{"instance_type": testLargeSize, "strategy": "lowest-price-per-core"}, 1)
}

func TestASizedLeaseCreatesTheMachineItSelected(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = offering(candidate(testLargeSize, 8, 16, 0.06), candidate(testSize, 2, 4, 0.02))
	h.settle()

	h.assertMachineSizes(testSize)

	h.deleteLease()
	h.settle()
}

func TestAnUnconfirmedLeaseStillCreatesTheMachineItsSpecPins(t *testing.T) {
	h := newHarness(t, sized(unvalidatedSize))
	h.catalogue = catalogue.NewCache()
	h.settle()

	h.assertLatchedType("")
	h.assertMachineSizes(unvalidatedSize)

	h.deleteLease()
	h.settle()
}

func TestALatchedInstanceTypeOutlivesACatalogueThatWouldNowChooseAnother(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = offering(candidate(testSize, 2, 4, 0.02), candidate(testLargeSize, 8, 16, 0.06))
	h.settle()
	h.assertLatchedType(testSize)

	h.catalogue = offering(candidate(testSize, 2, 4, 0.02), candidate(testLargeSize, 8, 16, 0.001))
	h.settle()

	h.assertLatchedType(testSize)
	h.assertMachineSizes(testSize)
	if got := h.selection().Chosen; got != testSize {
		t.Errorf("the recorded selection now names %q, want %q", got, testSize)
	}
	if got := len(h.selectionEvents()); got != 1 {
		t.Errorf("the lease announced %d selections, want exactly one", got)
	}
	h.assertCounter(instanceTypeSelectedMetric,
		map[string]string{"instance_type": testLargeSize, "strategy": "lowest-price"}, 0)

	h.deleteLease()
	h.settle()
}

func TestAPinnedLeaseIsCountedAsAPinnedSelection(t *testing.T) {
	h := newHarness(t)

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused a pinned lease: %v", err)
	}

	h.assertCounter(instanceTypeSelectedMetric,
		map[string]string{"instance_type": testSize, "strategy": "pinned"}, 1)
}

func TestReadySecondsCarriesTheStrategyThatChoseTheMachine(t *testing.T) {
	h := newHarness(t, requiringCapacity(func(r *v1alpha1.SizeRequirements) {
		r.Strategy = v1alpha1.StrategyLowestPricePerCore
	}))
	h.catalogue = offering(candidate(testLargeSize, 8, 16, 0.06), candidate(testSize, 2, 4, 0.02))
	h.becomeReady()

	h.assertObservations(leaseReadySecondsMetric,
		map[string]string{"instance_type": testLargeSize, "selection": "lowest-price-per-core"}, 1, 0)

	h.deleteLease()
	h.settle()
}

func TestRequirementsNoInstanceTypeCanMeetAreRejected(t *testing.T) {
	h := newHarness(t, requiringCapacity(func(r *v1alpha1.SizeRequirements) { r.MinCPU = 64 }))
	h.catalogue = offering(
		candidate(testSize, 2, 4, 0.02),
		candidate("fake-arm-small", 2, 4, 0.001, onARM),
		candidate("fake-arm-large", 8, 16, 0.002, onARM),
	)

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted requirements no instance type can meet")
	}

	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonUnsatisfiedRequirements, testRegion)
	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonUnsatisfiedRequirements, "3 offered")
	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonUnsatisfiedRequirements, "Architecture 2, Cores 1")
	h.assertUnaccepted()
	h.assertCounter(selectionFailedMetric,
		map[string]string{"strategy": "lowest-price", "reason": "no_match"}, 1)
}

func TestARequirementsLeaseIsRejectedWhileTheCatalogueIsCold(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = catalogue.NewCache()

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance sized a lease against a catalogue that was never filled")
	}

	h.assertUnaccepted()
	h.assertCounter(selectionFailedMetric,
		map[string]string{"strategy": "lowest-price", "reason": "catalogue_unavailable"}, 1)
}

func TestALeaseInARegionTheCatalogueKnowsNothingAboutIsRejected(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = offering(candidate(testSize, 2, 4, 0.02, func(it *provider.InstanceType) { it.Region = "fake-b" }))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance sized a lease in a region the catalogue holds no rows for")
	}

	h.assertUnaccepted()
	h.assertCounter(selectionFailedMetric,
		map[string]string{"strategy": "lowest-price", "reason": "region_unavailable"}, 1)
}

func TestARejectedLeaseCountsOneSelectionFailureHoweverOftenItIsReconciled(t *testing.T) {
	h := newHarness(t, requiringCapacity(func(r *v1alpha1.SizeRequirements) { r.MinCPU = 64 }))
	h.catalogue = offering(candidate(testSize, 2, 4, 0.02))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted requirements no instance type can meet")
	}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("the replayed acceptance pass stopped failing")
	}

	h.assertCounter(selectionFailedMetric,
		map[string]string{"strategy": "lowest-price", "reason": "no_match"}, 1)
}

func TestASizedLeaseRecordsTheDecisionThatChoseItsMachine(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = offering(
		candidate(testLargeSize, 8, 16, runnerUpRate),
		candidate(testSize, 2, 4, chosenRate),
		candidate("fake-arm-small", 2, 4, 0.001, onARM),
		candidate("fake-arm-large", 8, 16, 0.002, onARM),
	)

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused a lease its requirements can be met: %v", err)
	}

	recorded := h.selection()
	if recorded.Strategy != v1alpha1.StrategyLowestPrice {
		t.Errorf("selection records strategy %q, want %q", recorded.Strategy, v1alpha1.StrategyLowestPrice)
	}
	if recorded.Chosen != testSize {
		t.Errorf("selection records chosen %q, want %q", recorded.Chosen, testSize)
	}
	if recorded.HourlyRate != "0.00390625" || recorded.Currency != "EUR" {
		t.Errorf("selection records a rate of %q %q, want %q %q",
			recorded.HourlyRate, recorded.Currency, "0.00390625", "EUR")
	}
	if recorded.RunnerUp != testLargeSize {
		t.Errorf("selection records runner-up %q, want %q", recorded.RunnerUp, testLargeSize)
	}
	if recorded.Offered != 4 {
		t.Errorf("selection records %d offered candidates, want %d", recorded.Offered, 4)
	}
	if recorded.Qualified != 2 {
		t.Errorf("selection records %d qualifying candidates, want %d", recorded.Qualified, 2)
	}
	want := []v1alpha1.RejectedCandidates{{Reason: string(rejectedArchitecture), Count: 2}}
	if !slices.Equal(recorded.Rejected, want) {
		t.Errorf("selection records rejections %v, want %v", recorded.Rejected, want)
	}
	if recorded.DecidedAt.IsZero() {
		t.Error("selection records no decision time")
	}
}

func TestASizedLeaseAnnouncesWhatItChoseAndWhatItBeat(t *testing.T) {
	h := newHarness(t, requiringCapacity())
	h.catalogue = offering(
		candidate(testLargeSize, 8, 16, runnerUpRate),
		candidate(testSize, 2, 4, chosenRate),
		candidate("fake-arm-small", 2, 4, 0.001, onARM),
		candidate("fake-arm-large", 8, 16, 0.002, onARM),
	)

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused a lease its requirements can be met: %v", err)
	}

	h.assertAnnouncedSelection(
		"Normal InstanceTypeSelected LowestPrice chose fake-small from 2 of 4 offered candidates, runner-up fake-large")
	h.assertLogged("selected an instance type from requirements",
		`"instanceType"="fake-small"`,
		`"strategy"="LowestPrice"`,
		`"runnerUp"="fake-large"`,
		`"margin"=0.02734375`,
		`"offered"=4`,
		`"qualified"=2`,
		`"rejected"=[{"reason"="Architecture" "count"=2}]`,
	)
}

func TestAPinnedLeaseRecordsNoSelection(t *testing.T) {
	h := newHarness(t)

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused a pinned lease: %v", err)
	}

	if recorded := h.lease().Status.Selection; recorded != nil {
		t.Errorf("a pinned lease recorded the selection %+v, want none", *recorded)
	}
	h.assertAnnouncedNothing()
}

func TestARecordedSelectionIsNotOverwrittenByASecondResolve(t *testing.T) {
	recorder := events.NewFakeRecorder(testEventBufferLen)
	r := &CapacityLeaseReconciler{Clock: newStubClock().Now, Recorder: recorder}
	lease := &v1alpha1.CapacityLease{}

	first := selectInstanceType([]provider.InstanceType{candidate(testSize, 2, 4, 0.02)}, requirements())
	second := selectInstanceType([]provider.InstanceType{candidate(testLargeSize, 8, 16, 0.001)}, requirements())

	announced := 0
	for _, decision := range []selectionDecision{first, second} {
		if announce := r.latchSelection(t.Context(), lease, &decision); announce != nil {
			announce()
			announced++
		}
	}

	if got := lease.Status.Selection.Chosen; got != testSize {
		t.Errorf("the second resolve rewrote the selection to %q, want %q", got, testSize)
	}
	if announced != 1 {
		t.Errorf("the reconciler announced %d selections, want %d", announced, 1)
	}
}
