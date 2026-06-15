package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/spf13/cobra"
)

func resolveTypeCmd(t *testing.T, typeName string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("type", typeName, "")
	cmd.Flags().String("namespace", "", "")
	cmd.Flags().String("pool", "", "")
	cmd.Flags().Int32("replicas", 0, "")
	return cmd
}

func TestResolvePoolTargetDefaultsToReserved(t *testing.T) {
	app := newTestApp()
	name, poolType, err := cli.ResolvePoolTargetForTest(resolveTypeCmd(t, ""), app)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if name != "reserved-workers" || poolType != "reserved" {
		t.Errorf("default resolve = %q/%q, want reserved-workers/reserved", name, poolType)
	}
}

func TestResolvePoolTargetElastic(t *testing.T) {
	app := newTestApp()
	name, poolType, err := cli.ResolvePoolTargetForTest(resolveTypeCmd(t, "elastic"), app)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if name != "elastic-workers" || poolType != "elastic" {
		t.Errorf("elastic resolve = %q/%q, want elastic-workers/elastic", name, poolType)
	}
}

func TestResolvePoolTargetUnknownTypeErrors(t *testing.T) {
	app := newTestApp()
	if _, _, err := cli.ResolvePoolTargetForTest(resolveTypeCmd(t, "bogus"), app); err == nil {
		t.Fatal("expected error for unknown pool type")
	}
}

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
