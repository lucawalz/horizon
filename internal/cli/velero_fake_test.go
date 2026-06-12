package cli_test

import (
	"context"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

type fakeVeleroClient struct {
	backups  []velerov1.Backup
	restores []velerov1.Restore

	createBackupErr  error
	triggerBackupErr error
	createRestoreErr error
	listBackupsErr   error
	getBackupErr     error
	deleteBackupErr  error
	listRestoresErr  error
	getRestoreErr    error

	createdBackupSpec    velerov1.BackupSpec
	createdBackupName    string
	createdRestoreSpec   velerov1.RestoreSpec
	createdRestoreName   string
	triggeredBackupSpec  velerov1.BackupSpec
	triggeredRestoreSpec velerov1.RestoreSpec
	deletedBackup        string
	waited               bool
}

func (f *fakeVeleroClient) CreateBackup(_ context.Context, spec velerov1.BackupSpec, name string) error {
	f.createdBackupSpec = spec
	f.createdBackupName = name
	return f.createBackupErr
}

func (f *fakeVeleroClient) TriggerBackup(_ context.Context, spec velerov1.BackupSpec, name string, _, _ time.Duration) error {
	f.triggeredBackupSpec = spec
	f.createdBackupName = name
	f.waited = true
	return f.triggerBackupErr
}

func (f *fakeVeleroClient) CreateRestore(_ context.Context, spec velerov1.RestoreSpec, name string) error {
	f.createdRestoreSpec = spec
	f.createdRestoreName = name
	return f.createRestoreErr
}

func (f *fakeVeleroClient) TriggerRestore(_ context.Context, spec velerov1.RestoreSpec, name string, _, _ time.Duration) error {
	f.triggeredRestoreSpec = spec
	f.createdRestoreName = name
	f.waited = true
	return nil
}

func (f *fakeVeleroClient) ListBackups(_ context.Context) ([]velerov1.Backup, error) {
	return f.backups, f.listBackupsErr
}

func (f *fakeVeleroClient) GetBackup(_ context.Context, name string) (*velerov1.Backup, error) {
	if f.getBackupErr != nil {
		return nil, f.getBackupErr
	}
	for i := range f.backups {
		if f.backups[i].Name == name {
			return &f.backups[i], nil
		}
	}
	return &velerov1.Backup{}, nil
}

func (f *fakeVeleroClient) DeleteBackup(_ context.Context, name string) error {
	f.deletedBackup = name
	return f.deleteBackupErr
}

func (f *fakeVeleroClient) ListRestores(_ context.Context) ([]velerov1.Restore, error) {
	return f.restores, f.listRestoresErr
}

func (f *fakeVeleroClient) GetRestore(_ context.Context, name string) (*velerov1.Restore, error) {
	if f.getRestoreErr != nil {
		return nil, f.getRestoreErr
	}
	for i := range f.restores {
		if f.restores[i].Name == name {
			return &f.restores[i], nil
		}
	}
	return &velerov1.Restore{}, nil
}
