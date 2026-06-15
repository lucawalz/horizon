package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestDownScalesToZero(t *testing.T) {
	cc := capiClient(t, machineDeployment("caph-system", "burst-workers", "burst", 3))

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 0)
	if err := cli.RunDownForTest(context.Background(), cc, target, false, false); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	got, err := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want 0", got.Spec.Replicas)
	}
}

func TestDownDeleteRemovesPool(t *testing.T) {
	cc := capiClient(t, machineDeployment("caph-system", "burst-workers", "burst", 3))

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 0)
	if err := cli.RunDownForTest(context.Background(), cc, target, false, true); err != nil {
		t.Fatalf("RunDownForTest delete: %v", err)
	}

	if _, err := cc.GetPool(context.Background(), "caph-system", "burst-workers"); !apierrors.IsNotFound(err) {
		t.Errorf("GetPool after delete = %v, want IsNotFound", err)
	}
}

func TestDownDryRunDoesNotMutate(t *testing.T) {
	cc := capiClient(t, machineDeployment("caph-system", "burst-workers", "burst", 3))

	target := cli.PoolTargetForTest("caph-system", "burst-workers", "burst", 0)
	out := captureStdout(func() {
		if err := cli.RunDownForTest(context.Background(), cc, target, true, false); err != nil {
			t.Errorf("RunDownForTest dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "scale pool caph-system/burst-workers to 0") {
		t.Errorf("dry-run output missing scale intent:\n%s", out)
	}

	got, _ := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
		t.Errorf("dry-run must not mutate replicas: %v", got.Spec.Replicas)
	}
}
