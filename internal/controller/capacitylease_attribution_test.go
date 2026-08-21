package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	patchedAfterAcceptance = "patched-after-acceptance-with-a-long-string"
	unvalidatedSize        = "unvalidated-size-with-a-long-recognisable-tag"
	missingProviderConfig  = "missing-provider-config-with-a-long-name-tag"
)

func referencing(config string) func(*v1alpha1.CapacityLease) {
	return func(lease *v1alpha1.CapacityLease) { lease.Spec.ProviderRef = config }
}

func (h *harness) repointLease(config, region, size string) {
	h.t.Helper()
	lease := h.lease()
	lease.Spec.ProviderRef = config
	lease.Spec.Region = region
	lease.Spec.Size = size
	if err := h.api.Update(h.t.Context(), lease); err != nil {
		h.t.Fatalf("repoint the accepted lease: %v", err)
	}
}

func TestNothingPatchedOntoAnAcceptedLeaseReachesALabel(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()

	h.createProviderConfig(patchedAfterAcceptance)
	h.repointLease(patchedAfterAcceptance, patchedAfterAcceptance, patchedAfterAcceptance)

	h.deleteLease()
	h.settle()

	assertNoSeriesCarries(t, patchedAfterAcceptance)
	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": testSize, "path": "controller"}, 1)
	h.assertCounter(leaseTerminalMetric, map[string]string{"outcome": "released"}, 1)
}

func TestAColdCatalogueKeepsAnUnvalidatedSizeOutOfEveryLabel(t *testing.T) {
	h := newHarness(t, sized(unvalidatedSize))
	h.catalogue = catalogue.NewCache()
	h.becomeReady()

	if got := h.lease().Status.InstanceType; got != "" {
		t.Errorf("a lease admitted against a cold catalogue latched instance type %q", got)
	}

	h.deleteLease()
	h.settle()

	assertNoSeriesCarries(t, unvalidatedSize)
	h.assertCounter(instanceReleasedMetric, map[string]string{"instance_type": "", "path": "controller"}, 1)
}

func TestTheInstanceTypeIsLatchedOnceTheCatalogueCanConfirmIt(t *testing.T) {
	h := newHarness(t)
	h.catalogue = catalogue.NewCache()

	if err := h.acceptancePass(); err != nil {
		t.Fatalf("acceptance refused work while the catalogue was cold: %v", err)
	}
	if got := h.lease().Status.InstanceType; got != "" {
		t.Fatalf("a lease admitted against a cold catalogue latched instance type %q", got)
	}

	h.catalogue = stubCatalogue{types: []provider.InstanceType{offeredType(testSize, testRegion, true)}}
	h.settle()

	if got := h.lease().Status.InstanceType; got != testSize {
		t.Errorf("instanceType is %q once the catalogue filled, want %q", got, testSize)
	}
}

func TestALeaseRejectedForItsRegionIsCountedUnderAPlaceholder(t *testing.T) {
	h := newHarness(t, placedIn("fake-z"))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted a region the provider does not offer")
	}

	if rejected := gatheredSeries(t, leaseTerminalMetric, map[string]string{"region": "fake-z"}); rejected != nil {
		t.Errorf("a lease rejected for its region minted a series under that region: %v", rejected)
	}
	h.assertCounterAt(leaseTerminalMetric,
		map[string]string{"provider": h.name, "region": unknownLabel, "outcome": "rejected"}, 1)
}

func TestALeaseNamingAnUnknownProviderConfigIsCountedAsRejected(t *testing.T) {
	h := newHarness(t, referencing(missingProviderConfig))

	if err := h.acceptancePass(); err == nil {
		t.Fatal("acceptance admitted a lease naming a provider config that does not exist")
	}

	h.assertCondition(v1alpha1.ConditionAccepted, metav1.ConditionFalse)
	h.assertCounterAt(leaseTerminalMetric,
		map[string]string{"provider": unknownLabel, "region": unknownLabel, "outcome": "rejected"}, 1)
	assertNoSeriesCarries(t, missingProviderConfig)
}
