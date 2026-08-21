package controller

import (
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

const (
	watchdogRenewalsMetric = "horizon_watchdog_renewals_total"
	orphanDeletedMetric    = "horizon_orphan_instances_deleted_total"

	relatchedApartFromTheSpec = "relatched-apart-from-the-spec-with-a-long-tag"
)

func (h *harness) assertRenewals(result string, want float64) {
	h.t.Helper()
	h.assertCounterAt(watchdogRenewalsMetric, map[string]string{"provider": h.name, "result": result}, want)
}

func TestArmingANodeForTheFirstTimeCountsAWatchdogRenewal(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()

	h.assertRenewals("success", 1)
	h.assertRenewals("failure", 0)
}

func TestADeadlineWithSlackToSpareIsNotCountedAsARenewal(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()
	h.settle()

	h.assertRenewals("success", 1)
}

func TestARenewalIsCountedAgainstTheLatchedProviderConfigNotTheSpec(t *testing.T) {
	h := newHarness(t)
	h.becomeReady()

	h.relatchProviderConfig(relatchedApartFromTheSpec)
	h.clock.Advance(testRenewInterval)
	h.settle()

	h.assertCounterAt(watchdogRenewalsMetric,
		map[string]string{"provider": relatchedApartFromTheSpec, "result": "success"}, 1)
	h.assertRenewals("success", 1)
}

func TestARenewalTheApiserverRefusesIsCountedAsAFailure(t *testing.T) {
	h := newHarness(t)
	h.settle()
	h.joinNode(h.instanceName(0), true)
	h.kube.PrependReactor("patch", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("fake: the apiserver refused the node patch")
	})

	if _, err := h.reconcile(); err == nil {
		t.Fatal("the reconcile hid a refused watchdog renewal")
	}

	h.assertRenewals("failure", 1)
	h.assertRenewals("success", 0)
}

func (f *orphanFixture) assertOrphanDeletions(region string, want float64) {
	f.t.Helper()
	labels := map[string]string{"provider": f.config, "region": region}
	if got := f.counter(orphanDeletedMetric, labels); got != want {
		f.t.Errorf("the sweep counted %v orphan deletions in %q, want %v", got, region, want)
	}
}

func TestSweepingAnExpiredInstanceCountsAnOrphanDeletion(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("swept", string(lease.UID), f.instant.Add(-orphanExpiryGrace-time.Minute))
	f.deleteLease(lease)

	f.mustSweep()

	f.assertOrphanDeletions(orphanTestRegion, 1)
	f.assertOrphanDeletions("nbg1", 0)
	f.assertNoLeaks()
}

func TestAnInstanceThatSurvivesItsDeleteIsNotCountedAsAnOrphanDeletion(t *testing.T) {
	f := newOrphanFixture(t)
	lease := f.createLease("gone")
	f.createInstance("stubborn", string(lease.UID), f.instant.Add(-time.Hour))
	f.deleteLease(lease)
	f.provider.FailDelete = func(string) error { return errProviderUnavailable }

	if err := f.sweep(); err == nil {
		t.Fatal("sweep reported success while the delete failed")
	}
	f.assertOrphanDeletions(orphanTestRegion, 0)

	f.provider.FailDelete = nil
	f.mustSweep()

	f.assertOrphanDeletions(orphanTestRegion, 1)
	f.assertNoLeaks()
}
