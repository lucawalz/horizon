package controller

import (
	"testing"
	"time"
)

func TestWatchdogArmedStalenessWindowScalesWithARenewInterval(t *testing.T) {
	renew := 5 * time.Second
	policy := testPolicy(renew, testSlack)
	want := renew * watchdogArmedStalenessRenewIntervalMultiple
	if want >= watchdogArmedStalenessCeiling {
		t.Fatalf("test setup error: %s is not below the ceiling %s", want, watchdogArmedStalenessCeiling)
	}

	if got := watchdogArmedStalenessWindow(policy); got != want {
		t.Errorf("staleness window = %s, want the unclamped %s", got, want)
	}
}

func TestWatchdogArmedStalenessWindowIsClampedToTheCeiling(t *testing.T) {
	policy := testPolicy(20*time.Minute, testSlack)

	got := watchdogArmedStalenessWindow(policy)

	if got != watchdogArmedStalenessCeiling {
		t.Errorf("staleness window = %s, want it clamped to the ceiling %s", got, watchdogArmedStalenessCeiling)
	}
	if unclamped := 20 * time.Minute * watchdogArmedStalenessRenewIntervalMultiple; got >= unclamped {
		t.Errorf("staleness window = %s, want it below the unclamped %s", got, unclamped)
	}
}
