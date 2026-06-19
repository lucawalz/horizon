package core

import (
	"context"
	"fmt"
	"time"

	"github.com/lucawalz/horizon/internal/hcloud"
	"github.com/lucawalz/horizon/internal/k8s"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	burstNodePoll      = 5 * time.Second
	burstNodeTimeout   = 5 * time.Minute
	burstWorkloadPoll  = 5 * time.Second
	burstWorkloadWait  = 5 * time.Minute
	burstBackupPoll    = 5 * time.Second
	burstBackupTimeout = 10 * time.Minute
	rollbackTimeout    = 30 * time.Second

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
	CreateSchedule(ctx context.Context, spec velerov1.ScheduleSpec, name string) error
	ListSchedules(ctx context.Context) ([]velerov1.Schedule, error)
	GetSchedule(ctx context.Context, name string) (*velerov1.Schedule, error)
	DeleteSchedule(ctx context.Context, name string) error
	ListBackupStorageLocations(ctx context.Context) ([]velerov1.BackupStorageLocation, error)
	CreateBackupStorageLocation(ctx context.Context, spec velerov1.BackupStorageLocationSpec, name string) error
}

type BurstParams struct {
	Target   PoolTarget
	Workload string
	PoolNode string
}

func Burst(ctx context.Context, hc *hcloud.Client, spec hcloud.ServerSpec, kc kubernetes.Interface, vc VeleroClient, p BurstParams, progress Progress) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p.Target.PoolType == ElasticPoolType {
		return ElasticAutoscalerErr()
	}

	prior, err := hc.ListReservedServers(ctx)
	if err != nil {
		return fmt.Errorf("list reserved servers: %w", err)
	}
	priorCount := len(prior)

	backupName := fmt.Sprintf("horizon-burst-%s-%d", p.Workload, time.Now().Unix())
	backupSpec := velerov1.BackupSpec{IncludedNamespaces: []string{p.Workload}, StorageLocation: DefaultStorageLocation}
	progress.Debug("phase backup: namespace " + p.Workload)
	if err := vc.TriggerBackup(ctx, backupSpec, backupName, burstBackupPoll, burstBackupTimeout); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	scaled := false
	defer func() {
		if err == nil || !scaled {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_, _ = hc.ScaleReservedTo(rbCtx, spec, priorCount)
	}()

	want := int(p.Target.Replicas)
	progress.Debug(fmt.Sprintf("phase scale: reserved servers -> %d", want))
	if _, err := hc.ScaleReservedTo(ctx, spec, want); err != nil {
		return fmt.Errorf("scale reserved: %w", err)
	}
	scaled = true

	progress.Debug("phase wait: reserved nodes ready")
	if err := k8s.WaitReservedNodesReady(ctx, kc, hcloud.ReservedPoolValue, want, burstNodePoll, burstNodeTimeout); err != nil {
		return fmt.Errorf("wait nodes: %w", err)
	}

	progress.Debug("phase migrate: workload " + p.Workload)
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

	progress.Emit(fmt.Sprintf("Burst complete: reserved pool scaled to %d, workload %q migrated", want, p.Workload))
	return nil
}
