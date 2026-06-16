package cli

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/spf13/cobra"
)

type poolTarget = core.PoolTarget

func resolvePoolTarget(cmd *cobra.Command, app *App) (poolTarget, error) {
	poolType, _ := cmd.Flags().GetString("type")
	name, err := app.Config.Pools.Resolve(poolType)
	if err != nil {
		return poolTarget{}, err
	}
	if poolType == "" {
		poolType = app.Config.Pools.DefaultType
	}

	t := poolTarget{
		Namespace: app.Config.Pools.Namespace,
		Name:      name,
		PoolType:  poolType,
		Cluster:   app.Cluster,
		Replicas:  1,
	}
	if v, _ := cmd.Flags().GetString("namespace"); v != "" {
		t.Namespace = v
	}
	if v, _ := cmd.Flags().GetString("pool"); v != "" {
		t.Name = v
	}
	if v, _ := cmd.Flags().GetInt32("replicas"); v > 0 {
		t.Replicas = v
	}
	return t, nil
}

func newUpCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Scale the worker MachineDeployment up to add nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := resolvePoolTarget(cmd, app)
			if err != nil {
				return fmt.Errorf("up: %w", err)
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			nudge, _ := cmd.Flags().GetBool("nudge")
			return runUp(cmd.Context(), app.CapiClient, target, dryRun, nudge)
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print target pool and desired replicas without scaling")
	cmd.Flags().Bool("nudge", false, "Latch the externally-managed control-plane-initialized status when not yet set")
	cmd.Flags().String("type", "", "Pool type to target (default from config)")
	cmd.Flags().String("namespace", "", "Override the pool namespace")
	cmd.Flags().String("pool", "", "Override the MachineDeployment name")
	cmd.Flags().Int32("replicas", 0, "Desired replica count (default 1)")
	return cmd
}

func runUp(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, nudge bool) error {
	if err := core.ScaleUp(ctx, cc, target, dryRun, nudge, printlnProgress); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	return nil
}

func printlnProgress(msg string) { fmt.Println(msg) }

func PoolTargetForTest(namespace, name, cluster string, replicas int32) poolTarget {
	return poolTarget{Namespace: namespace, Name: name, Cluster: cluster, Replicas: replicas}
}

func ResolvePoolTargetForTest(cmd *cobra.Command, app *App) (string, string, error) {
	t, err := resolvePoolTarget(cmd, app)
	return t.Name, t.PoolType, err
}

func RunUpForTest(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, nudge bool) error {
	return runUp(ctx, cc, target, dryRun, nudge)
}
