package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

func sized(size string) func(*v1alpha1.CapacityLease) {
	return func(lease *v1alpha1.CapacityLease) { lease.Spec.Size = size }
}

func placedIn(region string) func(*v1alpha1.CapacityLease) {
	return func(lease *v1alpha1.CapacityLease) { lease.Spec.Region = region }
}

func (h *harness) acceptancePass() error {
	h.t.Helper()
	if _, err := h.reconcile(); err != nil {
		h.t.Fatalf("finalizer pass: %v", err)
	}
	_, err := h.reconcile()
	return err
}

func (h *harness) assertUnaccepted() {
	h.t.Helper()
	lease := h.lease()
	if lease.Status.AcceptedAt != nil {
		h.t.Errorf("a rejected lease recorded an acceptance time of %s", lease.Status.AcceptedAt)
	}
	if lease.Status.InstanceType != "" {
		h.t.Errorf("a rejected lease latched instance type %q", lease.Status.InstanceType)
	}
	h.assertProviderEmpty()
}

func TestAcceptanceRejectsAnInstanceTypeTheCatalogueDoesNotOffer(t *testing.T) {
	h := newHarness(t, sized("fake-mistyped"))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted an instance type the catalogue does not offer")
	}

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionFalse)
	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonUnknownInstanceType, "fake-mistyped")
	h.assertUnaccepted()
}

func TestAcceptanceRejectsAnInstanceTypeOfferedOnlyInAnotherRegion(t *testing.T) {
	h := newHarness(t)
	h.catalogue = stubCatalogue{types: []provider.InstanceType{offeredType(testSize, "fake-b", true)}}

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted an instance type offered only in another region")
	}

	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonUnknownInstanceType, testRegion)
	h.assertUnaccepted()
}

func TestAcceptanceAdmitsATypeThatIsOfferedButCurrentlyUnavailable(t *testing.T) {
	h := newHarness(t)
	h.catalogue = stubCatalogue{types: []provider.InstanceType{offeredType(testSize, testRegion, false)}}

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance rejected an offered type that is merely unavailable: %v", err)
	}

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionTrue)
	if got := h.lease().Status.InstanceType; got != testSize {
		t.Errorf("instanceType is %q, want %q", got, testSize)
	}
}

func TestAcceptanceProceedsWhileTheCatalogueHasNeverBeenFilled(t *testing.T) {
	h := newHarness(t)
	h.catalogue = catalogue.NewCache()

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused work while the catalogue was cold: %v", err)
	}

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionTrue)
}

func TestAcceptanceRejectsARegionTheProviderDoesNotOffer(t *testing.T) {
	h := newHarness(t, placedIn("fake-z"))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted a region the provider does not offer")
	}

	h.assertConditionDetail(v1alpha1.ConditionAccepted, reasonUnknownRegion, "fake-z")
	h.assertUnaccepted()
}

func TestARejectedLeaseCountsOneTerminalOutcomeHoweverOftenItIsReconciled(t *testing.T) {
	h := newHarness(t, sized("fake-mistyped"))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted an instance type the catalogue does not offer")
	}
	if _, err := h.reconcile(); err == nil {
		t.Fatal("the replayed acceptance pass stopped failing")
	}

	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "rejected"}, 1)
}

func TestALeaseRejectedForItsRegionIsNotCountedUnderThatRegion(t *testing.T) {
	h := newHarness(t, placedIn("fake-z"))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted a region the provider does not offer")
	}

	rejected := gatheredSeries(t, leaseTerminalMetric, map[string]string{"provider": h.name, "region": "fake-z"})
	if rejected != nil {
		t.Errorf("a lease rejected for its region minted a series under that region: %v", rejected)
	}
}

func TestTheLeaseControllerRefusesToStartWithoutACatalogue(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	mgr, err := ctrl.NewManager(testEnv.Config, ctrl.Options{
		Scheme:  testScheme(),
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}

	if err := (&CapacityLeaseReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err == nil {
		t.Fatal("the lease controller started without an instance type catalogue")
	}
	if err := (&CapacityLeaseReconciler{Client: mgr.GetClient(), Catalogue: catalogue.NewCache()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("the lease controller refused to start with a catalogue: %v", err)
	}
}
