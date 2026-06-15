package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

func TestUpScalesPool(t *testing.T) {
	cc := capiClient(t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", true),
	)

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 2)
	if err := cli.RunUpForTest(context.Background(), cc, target, false, false); err != nil {
		t.Fatalf("RunUpForTest: %v", err)
	}

	got, err := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", got.Spec.Replicas)
	}
}

func TestUpRefusesWhenControlPlaneNotInitialized(t *testing.T) {
	cc := capiClient(t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", false),
	)

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 1)
	err := cli.RunUpForTest(context.Background(), cc, target, false, false)
	if err == nil {
		t.Fatal("expected refusal when control plane not initialized")
	}
	if !strings.Contains(err.Error(), "--nudge") {
		t.Errorf("error %q should mention --nudge", err.Error())
	}

	got, err := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("pool must not be scaled on refusal: %v", got.Spec.Replicas)
	}
}

func TestUpNudgeLatchesAndScales(t *testing.T) {
	cc := capiClient(t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", false),
	)

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 1)
	if err := cli.RunUpForTest(context.Background(), cc, target, false, true); err != nil {
		t.Fatalf("RunUpForTest with nudge: %v", err)
	}

	ok, err := cc.IsControlPlaneInitialized(context.Background(), "caph-system", "burst")
	if err != nil {
		t.Fatalf("IsControlPlaneInitialized: %v", err)
	}
	if !ok {
		t.Error("nudge must latch control-plane-initialized")
	}
	got, _ := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 1 {
		t.Errorf("replicas = %v, want 1", got.Spec.Replicas)
	}
}

func TestUpFailsFastWhenPoolMissing(t *testing.T) {
	cc := capiClient(t, initializedCluster("caph-system", "burst", true))

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 1)
	err := cli.RunUpForTest(context.Background(), cc, target, false, false)
	if err == nil {
		t.Fatal("expected fail-fast when pool not found")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "bedrock") {
		t.Errorf("error %q should explain the home pool is GitOps-managed in bedrock", err.Error())
	}
}

func TestUpDryRunDoesNotMutate(t *testing.T) {
	cc := capiClient(t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", false),
	)

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 3)
	out := captureStdout(func() {
		if err := cli.RunUpForTest(context.Background(), cc, target, true, false); err != nil {
			t.Errorf("RunUpForTest dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "0 -> 3") {
		t.Errorf("dry-run output missing replica delta:\n%s", out)
	}

	got, _ := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("dry-run must not mutate replicas: %v", got.Spec.Replicas)
	}
}
