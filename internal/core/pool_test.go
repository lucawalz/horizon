package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestScaleUpScalesPool(t *testing.T) {
	cc := capiClient(
		t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", true),
	)

	target := poolTarget("caph-system", "burst-workers", "burst", 2)
	if err := core.ScaleUp(context.Background(), cc, target, false, core.Progress{}); err != nil {
		t.Fatalf("ScaleUp: %v", err)
	}

	got, err := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", got.Spec.Replicas)
	}
}

func TestScaleUpRefusesWhenControlPlaneNotInitialized(t *testing.T) {
	cc := capiClient(
		t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", false),
	)

	target := poolTarget("caph-system", "burst-workers", "burst", 1)
	err := core.ScaleUp(context.Background(), cc, target, false, core.Progress{})
	if err == nil {
		t.Fatal("expected refusal when control plane not initialized")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("error %q should explain the control plane is not initialized", err.Error())
	}

	got, _ := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("pool must not be scaled on refusal: %v", got.Spec.Replicas)
	}
}

func TestScaleUpFailsFastWhenPoolMissing(t *testing.T) {
	cc := capiClient(t, initializedCluster("caph-system", "burst", true))

	target := poolTarget("caph-system", "burst-workers", "burst", 1)
	err := core.ScaleUp(context.Background(), cc, target, false, core.Progress{})
	if err == nil {
		t.Fatal("expected fail-fast when pool not found")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "bedrock") {
		t.Errorf("error %q should explain the home pool is GitOps-managed in bedrock", err.Error())
	}
}

func TestScaleUpDryRunDoesNotMutate(t *testing.T) {
	cc := capiClient(
		t,
		machineDeployment("caph-system", "burst-workers", "burst", 0),
		initializedCluster("caph-system", "burst", false),
	)

	var msgs []string
	target := poolTarget("caph-system", "burst-workers", "burst", 3)
	if err := core.ScaleUp(context.Background(), cc, target, true, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleUp dry-run: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "0 -> 3") {
		t.Errorf("dry-run progress missing replica delta: %v", msgs)
	}

	got, _ := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("dry-run must not mutate replicas: %v", got.Spec.Replicas)
	}
}

func TestScaleUpElasticEmitsAutoscalerNote(t *testing.T) {
	cc := capiClient(
		t,
		machineDeployment("caph-system", "elastic-workers", "burst", 0),
		initializedCluster("caph-system", "burst", true),
	)

	var msgs []string
	target := core.PoolTarget{Namespace: "caph-system", Name: "elastic-workers", PoolType: core.ElasticPoolType, Cluster: "burst", Replicas: 2}
	if err := core.ScaleUp(context.Background(), cc, target, false, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleUp: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "cluster-autoscaler owns the elastic pool") {
		t.Errorf("expected autoscaler note for elastic pool: %v", msgs)
	}
}

func TestScaleDownScalesToZero(t *testing.T) {
	cc := capiClient(t, machineDeployment("caph-system", "burst-workers", "burst", 3))

	target := poolTarget("caph-system", "burst-workers", "burst", 0)
	if err := core.ScaleDown(context.Background(), cc, target, false, false, core.Progress{}); err != nil {
		t.Fatalf("ScaleDown: %v", err)
	}

	got, err := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want 0", got.Spec.Replicas)
	}
}

func TestScaleDownDeleteRemovesPool(t *testing.T) {
	cc := capiClient(t, machineDeployment("caph-system", "burst-workers", "burst", 3))

	target := poolTarget("caph-system", "burst-workers", "burst", 0)
	if err := core.ScaleDown(context.Background(), cc, target, false, true, core.Progress{}); err != nil {
		t.Fatalf("ScaleDown delete: %v", err)
	}

	if _, err := cc.GetPool(context.Background(), "caph-system", "burst-workers"); !apierrors.IsNotFound(err) {
		t.Errorf("GetPool after delete = %v, want IsNotFound", err)
	}
}

func TestScaleDownDryRunDoesNotMutate(t *testing.T) {
	cc := capiClient(t, machineDeployment("caph-system", "burst-workers", "burst", 3))

	var msgs []string
	target := poolTarget("caph-system", "burst-workers", "burst", 0)
	if err := core.ScaleDown(context.Background(), cc, target, true, false, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleDown dry-run: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "scale pool caph-system/burst-workers to 0") {
		t.Errorf("dry-run progress missing scale intent: %v", msgs)
	}

	got, _ := cc.GetPool(context.Background(), "caph-system", "burst-workers")
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
		t.Errorf("dry-run must not mutate replicas: %v", got.Spec.Replicas)
	}
}
