package cli

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/spf13/cobra"
)

func newDownCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Scale the worker MachineDeployment to zero, or delete it with --delete",
		Long: "Scale the worker MachineDeployment to zero replicas. With --delete the MachineDeployment is removed entirely; " +
			"deletion suits horizon-owned pools, while the Flux-managed home pool is reconciled back from bedrock.",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := resolvePoolTarget(cmd, app)
			if err != nil {
				return fmt.Errorf("down: %w", err)
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			del, _ := cmd.Flags().GetBool("delete")
			return runDown(cmd.Context(), app.CapiClient, target, dryRun, del)
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print intent without scaling or deleting")
	cmd.Flags().Bool("delete", false, "Delete the MachineDeployment instead of scaling it to zero")
	cmd.Flags().String("type", "", "Pool type to target (default from config)")
	cmd.Flags().String("namespace", "", "Override the pool namespace")
	cmd.Flags().String("pool", "", "Override the MachineDeployment name")
	return cmd
}

func runDown(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, del bool) error {
	if err := core.ScaleDown(ctx, cc, target, dryRun, del, printlnProgress); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	return nil
}

func RunDownForTest(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, del bool) error {
	return runDown(ctx, cc, target, dryRun, del)
}
