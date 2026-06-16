package cli

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type burstParams = core.BurstParams

func newBurstCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burst",
		Short: "Back up, scale the pool up, and migrate a workload onto the new nodes",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			workload, _ := cmd.Flags().GetString("workload")
			if workload == "" {
				return fmt.Errorf("burst: --workload is required")
			}
			if err := k8s.ValidateNamespace(workload); err != nil {
				return fmt.Errorf("burst: %w", err)
			}
			target, err := resolvePoolTarget(cmd, app)
			if err != nil {
				return fmt.Errorf("burst: %w", err)
			}
			if target.Replicas < 1 {
				target.Replicas = 1
			}
			vc, err := resolveVeleroClient(app)
			if err != nil {
				return fmt.Errorf("burst: %w", err)
			}
			params := burstParams{Target: target, Workload: workload, PoolNode: target.PoolType}
			return runBurst(cmd.Context(), app.CapiClient, app.KubeClient, vc, params)
		},
	}
	cmd.Flags().String("workload", "", "target namespace to burst (required)")
	cmd.Flags().String("type", "", "Pool type to target (default from config)")
	cmd.Flags().String("namespace", "", "Override the pool namespace")
	cmd.Flags().String("pool", "", "Override the MachineDeployment name")
	cmd.Flags().Int32("replicas", 0, "Desired pool replica count (default 1)")
	return cmd
}

func runBurst(ctx context.Context, cc *capi.Client, kc kubernetes.Interface, vc veleroClient, p burstParams) error {
	if err := core.Burst(ctx, cc, kc, vc, p, printlnProgress); err != nil {
		return fmt.Errorf("burst: %w", err)
	}
	return nil
}

func BurstParamsForTest(target poolTarget, workload, poolNode string) burstParams {
	return burstParams{Target: target, Workload: workload, PoolNode: poolNode}
}

func RunBurstForTest(ctx context.Context, cc *capi.Client, kc kubernetes.Interface, vc veleroClient, p burstParams) error {
	return runBurst(ctx, cc, kc, vc, p)
}

func NewBurstCmdForTest(app *App) *cobra.Command { return newBurstCmd(app) }
