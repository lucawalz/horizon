package cli

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	if ctx == nil {
		ctx = context.Background()
	}

	if dryRun {
		if del {
			fmt.Printf("[dry-run] delete pool %s/%s\n", target.namespace, target.name)
		} else {
			fmt.Printf("[dry-run] scale pool %s/%s to 0 replicas\n", target.namespace, target.name)
		}
		fmt.Println("[dry-run] No actions executed.")
		return nil
	}

	if del {
		if err := cc.DeletePool(ctx, target.namespace, target.name); err != nil {
			if apierrors.IsNotFound(err) {
				return notFoundPoolErr(target)
			}
			return fmt.Errorf("down: %w", err)
		}
		fmt.Printf("Deleted pool %s/%s\n", target.namespace, target.name)
		return nil
	}

	if err := cc.ScalePool(ctx, target.namespace, target.name, 0); err != nil {
		if apierrors.IsNotFound(err) {
			return notFoundPoolErr(target)
		}
		return fmt.Errorf("down: %w", err)
	}
	fmt.Printf("Scaled pool %s/%s to 0 replicas\n", target.namespace, target.name)
	return nil
}

func RunDownForTest(ctx context.Context, cc *capi.Client, target poolTarget, dryRun, del bool) error {
	return runDown(ctx, cc, target, dryRun, del)
}
