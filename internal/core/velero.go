package core

import (
	"context"
	"fmt"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

const (
	DefaultBackupTTL     = 168 * time.Hour
	BackupTimeLayout     = "2006-01-02 15:04"
	BackupNameTimeLayout = "20060102-150405"

	veleroWaitPoll    = 5 * time.Second
	veleroWaitTimeout = 10 * time.Minute
)

func DefaultBackupName(includeNs []string, now time.Time) string {
	scope := "all"
	if len(includeNs) > 0 {
		scope = includeNs[0]
	}
	return fmt.Sprintf("horizon-%s-%s", scope, now.UTC().Format(BackupNameTimeLayout))
}

func DefaultRestoreName(backupName string, now time.Time) string {
	return fmt.Sprintf("horizon-restore-%s-%s", backupName, now.UTC().Format(BackupNameTimeLayout))
}

func CreateBackup(ctx context.Context, vc VeleroClient, spec velerov1.BackupSpec, name string, wait bool) error {
	if wait {
		return vc.TriggerBackup(ctx, spec, name, veleroWaitPoll, veleroWaitTimeout)
	}
	return vc.CreateBackup(ctx, spec, name)
}

func CreateRestore(ctx context.Context, vc VeleroClient, spec velerov1.RestoreSpec, name string, wait bool) error {
	if wait {
		return vc.TriggerRestore(ctx, spec, name, veleroWaitPoll, veleroWaitTimeout)
	}
	return vc.CreateRestore(ctx, spec, name)
}

func DeleteBackup(ctx context.Context, vc VeleroClient, name string) error {
	return vc.DeleteBackup(ctx, name)
}

func ListBackups(ctx context.Context, vc VeleroClient) ([]velerov1.Backup, error) {
	return vc.ListBackups(ctx)
}

func GetBackup(ctx context.Context, vc VeleroClient, name string) (*velerov1.Backup, error) {
	return vc.GetBackup(ctx, name)
}

func ListRestores(ctx context.Context, vc VeleroClient) ([]velerov1.Restore, error) {
	return vc.ListRestores(ctx)
}

func GetRestore(ctx context.Context, vc VeleroClient, name string) (*velerov1.Restore, error) {
	return vc.GetRestore(ctx, name)
}

func CreateSchedule(ctx context.Context, vc VeleroClient, spec velerov1.ScheduleSpec, name string) error {
	return vc.CreateSchedule(ctx, spec, name)
}

func ListSchedules(ctx context.Context, vc VeleroClient) ([]velerov1.Schedule, error) {
	return vc.ListSchedules(ctx)
}

func GetSchedule(ctx context.Context, vc VeleroClient, name string) (*velerov1.Schedule, error) {
	return vc.GetSchedule(ctx, name)
}

func DeleteSchedule(ctx context.Context, vc VeleroClient, name string) error {
	return vc.DeleteSchedule(ctx, name)
}

func ListBackupStorageLocations(ctx context.Context, vc VeleroClient) ([]velerov1.BackupStorageLocation, error) {
	return vc.ListBackupStorageLocations(ctx)
}

func CreateBackupStorageLocation(ctx context.Context, vc VeleroClient, spec velerov1.BackupStorageLocationSpec, name string) error {
	return vc.CreateBackupStorageLocation(ctx, spec, name)
}
