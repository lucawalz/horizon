package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

const (
	burstMachinePoll    = 5 * time.Second
	burstMachineTimeout = 5 * time.Minute
	burstWorkloadPoll   = 5 * time.Second
	burstWorkloadWait   = 5 * time.Minute
	backupPoll          = 5 * time.Second
	backupTimeout       = 10 * time.Minute
	rollbackTimeout     = 30 * time.Second
)

type burstParams struct {
	target   poolTarget
	workload string
	poolNode string
}

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
			target := resolvePoolTarget(cmd, app)
			if target.replicas < 1 {
				target.replicas = 1
			}
			vc, err := resolveVeleroClient(app)
			if err != nil {
				return fmt.Errorf("burst: %w", err)
			}
			params := burstParams{target: target, workload: workload, poolNode: app.Config.Pools.Cluster}
			return runBurst(cmd.Context(), app.CapiClient, app.KubeClient, vc, params)
		},
	}
	cmd.Flags().String("workload", "", "target namespace to burst (required)")
	cmd.Flags().String("namespace", "", "Override the pool namespace")
	cmd.Flags().String("pool", "", "Override the MachineDeployment name")
	cmd.Flags().Int32("replicas", 0, "Desired pool replica count (default 1)")
	return cmd
}

func runBurst(ctx context.Context, cc *capi.Client, kc kubernetes.Interface, vc veleroClient, p burstParams) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	md, err := cc.GetPool(ctx, p.target.namespace, p.target.name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return notFoundPoolErr(p.target)
		}
		return fmt.Errorf("burst: get pool: %w", err)
	}
	priorReplicas := int32(0)
	if md.Spec.Replicas != nil {
		priorReplicas = *md.Spec.Replicas
	}

	backupName := fmt.Sprintf("horizon-burst-%s-%d", p.workload, time.Now().Unix())
	spec := velerov1.BackupSpec{IncludedNamespaces: []string{p.workload}, StorageLocation: defaultStorageLocation}
	if err := vc.TriggerBackup(ctx, spec, backupName, backupPoll, backupTimeout); err != nil {
		return fmt.Errorf("burst: backup: %w", err)
	}

	scaled := false
	defer func() {
		if err == nil || !scaled {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = cc.ScalePool(rbCtx, p.target.namespace, p.target.name, priorReplicas)
	}()

	if err := cc.ScalePool(ctx, p.target.namespace, p.target.name, p.target.replicas); err != nil {
		return fmt.Errorf("burst: scale pool: %w", err)
	}
	scaled = true

	if err := cc.WaitMachinesReady(ctx, p.target.namespace, p.target.name, p.target.replicas, burstMachinePoll, burstMachineTimeout); err != nil {
		return fmt.Errorf("burst: wait machines: %w", err)
	}

	var saved *k8s.SavedState
	saved, err = k8s.Migrate(ctx, kc, p.workload, p.poolNode)
	if err != nil {
		return fmt.Errorf("burst: migrate: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = k8s.RollbackMigrate(rbCtx, kc, saved)
	}()

	if err = k8s.WaitWorkloadOnBurstNodes(ctx, kc, p.workload, burstWorkloadPoll, burstWorkloadWait); err != nil {
		return fmt.Errorf("burst: wait workload: %w", err)
	}

	fmt.Printf("Burst complete: pool %s/%s scaled to %d, workload %q migrated\n",
		p.target.namespace, p.target.name, p.target.replicas, p.workload)
	return nil
}

func BurstParamsForTest(target poolTarget, workload, poolNode string) burstParams {
	return burstParams{target: target, workload: workload, poolNode: poolNode}
}

func RunBurstForTest(ctx context.Context, cc *capi.Client, kc kubernetes.Interface, vc veleroClient, p burstParams) error {
	return runBurst(ctx, cc, kc, vc, p)
}

func NewBurstCmdForTest(app *App) *cobra.Command { return newBurstCmd(app) }
