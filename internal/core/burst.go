package core

import (
	"context"
	"fmt"
	"time"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/k8s"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

const (
	burstMachinePoll    = 5 * time.Second
	burstMachineTimeout = 5 * time.Minute
	burstWorkloadPoll   = 5 * time.Second
	burstWorkloadWait   = 5 * time.Minute
	burstBackupPoll     = 5 * time.Second
	burstBackupTimeout  = 10 * time.Minute
	rollbackTimeout     = 30 * time.Second

	DefaultStorageLocation = "default"
)

type VeleroClient interface {
	CreateBackup(ctx context.Context, spec velerov1.BackupSpec, name string) error
	TriggerBackup(ctx context.Context, spec velerov1.BackupSpec, name string, poll, timeout time.Duration) error
	CreateRestore(ctx context.Context, spec velerov1.RestoreSpec, name string) error
	TriggerRestore(ctx context.Context, spec velerov1.RestoreSpec, name string, poll, timeout time.Duration) error
	ListBackups(ctx context.Context) ([]velerov1.Backup, error)
	GetBackup(ctx context.Context, name string) (*velerov1.Backup, error)
	DeleteBackup(ctx context.Context, name string) error
	ListRestores(ctx context.Context) ([]velerov1.Restore, error)
	GetRestore(ctx context.Context, name string) (*velerov1.Restore, error)
}

type BurstParams struct {
	Target   PoolTarget
	Workload string
	PoolNode string
}

func Burst(ctx context.Context, cc *capi.Client, kc kubernetes.Interface, vc VeleroClient, p BurstParams, progress Progress) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	md, err := cc.GetPool(ctx, p.Target.Namespace, p.Target.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return NotFoundPoolErr(p.Target.Namespace, p.Target.Name)
		}
		return fmt.Errorf("get pool: %w", err)
	}
	priorReplicas := int32(0)
	if md.Spec.Replicas != nil {
		priorReplicas = *md.Spec.Replicas
	}

	backupName := fmt.Sprintf("horizon-burst-%s-%d", p.Workload, time.Now().Unix())
	spec := velerov1.BackupSpec{IncludedNamespaces: []string{p.Workload}, StorageLocation: DefaultStorageLocation}
	if err := vc.TriggerBackup(ctx, spec, backupName, burstBackupPoll, burstBackupTimeout); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	scaled := false
	defer func() {
		if err == nil || !scaled {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = cc.ScalePool(rbCtx, p.Target.Namespace, p.Target.Name, priorReplicas)
	}()

	if err := cc.ScalePool(ctx, p.Target.Namespace, p.Target.Name, p.Target.Replicas); err != nil {
		return fmt.Errorf("scale pool: %w", err)
	}
	scaled = true

	if err := cc.WaitMachinesReady(ctx, p.Target.Namespace, p.Target.Name, p.Target.Replicas, burstMachinePoll, burstMachineTimeout); err != nil {
		return fmt.Errorf("wait machines: %w", err)
	}

	var saved *k8s.SavedState
	saved, err = k8s.Migrate(ctx, kc, p.Workload, p.PoolNode)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = k8s.RollbackMigrate(rbCtx, kc, saved)
	}()

	if err = k8s.WaitWorkloadOnBurstNodes(ctx, kc, p.Workload, burstWorkloadPoll, burstWorkloadWait); err != nil {
		return fmt.Errorf("wait workload: %w", err)
	}

	progress.emit(fmt.Sprintf("Burst complete: pool %s/%s scaled to %d, workload %q migrated",
		p.Target.Namespace, p.Target.Name, p.Target.Replicas, p.Workload))
	return nil
}
