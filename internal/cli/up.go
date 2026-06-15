package cli

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type poolTarget struct {
	namespace string
	name      string
	cluster   string
	replicas  int32
}

func resolvePoolTarget(cmd *cobra.Command, app *App) poolTarget {
	t := poolTarget{
		namespace: app.Config.Pools.Namespace,
		name:      app.Config.Pools.Name,
		cluster:   app.Config.Pools.Cluster,
		replicas:  1,
	}
	if v, _ := cmd.Flags().GetString("namespace"); v != "" {
		t.namespace = v
	}
	if v, _ := cmd.Flags().GetString("pool"); v != "" {
		t.name = v
	}
	if v, _ := cmd.Flags().GetString("cluster"); v != "" {
		t.cluster = v
	}
	if v, _ := cmd.Flags().GetInt32("replicas"); v > 0 {
		t.replicas = v
	}
	return t
}

func newUpCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Scale the worker MachineDeployment up to add nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			target := resolvePoolTarget(cmd, app)
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			nudge, _ := cmd.Flags().GetBool("nudge")
			return runUp(cmd.Context(), app.CapiClient, target, dryRun, nudge)
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print target pool and desired replicas without scaling")
	cmd.Flags().Bool("nudge", false, "Latch the externally-managed control-plane-initialized status when not yet set")
	cmd.Flags().String("namespace", "", "Override the pool namespace")
	cmd.Flags().String("pool", "", "Override the MachineDeployment name")
	cmd.Flags().String("cluster", "", "Override the cluster name")
	cmd.Flags().Int32("replicas", 0, "Desired replica count (default 1)")
	return cmd
}

func runUp(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, nudge bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if dryRun {
		md, err := cc.GetPool(ctx, target.namespace, target.name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return notFoundPoolErr(target)
			}
			return fmt.Errorf("up: get pool: %w", err)
		}
		current := int32(0)
		if md.Spec.Replicas != nil {
			current = *md.Spec.Replicas
		}
		fmt.Printf("[dry-run] pool %s/%s (cluster %s): %d -> %d replicas\n",
			target.namespace, target.name, target.cluster, current, target.replicas)
		fmt.Println("[dry-run] No actions executed.")
		return nil
	}

	initialized, err := cc.IsControlPlaneInitialized(ctx, target.namespace, target.cluster)
	if err != nil {
		return fmt.Errorf("up: control-plane status: %w", err)
	}
	if !initialized {
		if !nudge {
			return fmt.Errorf("up: control plane for cluster %q not initialized; rerun with --nudge to latch the externally-managed status", target.cluster)
		}
		if err := cc.NudgeControlPlaneInitialized(ctx, target.namespace, target.cluster); err != nil {
			return fmt.Errorf("up: %w", err)
		}
		fmt.Printf("Nudged control-plane-initialized for cluster %q.\n", target.cluster)
	}

	md, err := cc.GetPool(ctx, target.namespace, target.name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return notFoundPoolErr(target)
		}
		return fmt.Errorf("up: get pool: %w", err)
	}

	current := int32(0)
	if md.Spec.Replicas != nil {
		current = *md.Spec.Replicas
	}
	if current >= target.replicas {
		fmt.Printf("pool %s/%s already at %d replicas (>= %d); nothing to do\n",
			target.namespace, target.name, current, target.replicas)
		return nil
	}

	if err := cc.ScalePool(ctx, target.namespace, target.name, target.replicas); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	fmt.Printf("Scaled pool %s/%s: %d -> %d replicas\n",
		target.namespace, target.name, current, target.replicas)
	return nil
}

func notFoundPoolErr(target poolTarget) error {
	return fmt.Errorf("up: pool %s/%s not found; the home pool is provisioned via GitOps in bedrock, not by horizon",
		target.namespace, target.name)
}

func PoolTargetForTest(namespace, name, cluster string, replicas int32) poolTarget {
	return poolTarget{namespace: namespace, name: name, cluster: cluster, replicas: replicas}
}

func RunUpForTest(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, nudge bool) error {
	return runUp(ctx, cc, target, dryRun, nudge)
}
