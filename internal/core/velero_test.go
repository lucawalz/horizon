package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/core"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDefaultBackupName(t *testing.T) {
	now := fixedNow()
	if got := core.DefaultBackupName([]string{"sentio-systems"}, now); got != "horizon-sentio-systems-20260612-143005" {
		t.Errorf("name = %q", got)
	}
	if got := core.DefaultBackupName(nil, now); got != "horizon-all-20260612-143005" {
		t.Errorf("name = %q", got)
	}
}

func TestDefaultRestoreName(t *testing.T) {
	if got := core.DefaultRestoreName("bk1", fixedNow()); got != "horizon-restore-bk1-20260612-143005" {
		t.Errorf("name = %q", got)
	}
}

func TestCreateBackupWithoutWaitDoesNotTrigger(t *testing.T) {
	vc := &fakeVeleroClient{}
	spec := velerov1.BackupSpec{IncludedNamespaces: []string{"ns1"}}
	if err := core.CreateBackup(context.Background(), vc, spec, "my-backup", false); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if vc.createdBackupName != "my-backup" {
		t.Errorf("createdBackupName = %q, want my-backup", vc.createdBackupName)
	}
	if vc.waited {
		t.Error("create without wait must not trigger")
	}
}

func TestCreateBackupWaitTriggers(t *testing.T) {
	vc := &fakeVeleroClient{}
	spec := velerov1.BackupSpec{IncludedNamespaces: []string{"ns1"}}
	if err := core.CreateBackup(context.Background(), vc, spec, "wb", true); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if !vc.waited {
		t.Error("create with wait must call TriggerBackup")
	}
}

func TestListBackupsReturnsAll(t *testing.T) {
	older := velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "old-backup",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)),
		},
		Status: velerov1.BackupStatus{Phase: velerov1.BackupPhaseCompleted},
	}
	newer := velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "new-backup",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)),
		},
	}
	vc := &fakeVeleroClient{backups: []velerov1.Backup{older, newer}}

	got, err := core.ListBackups(context.Background(), vc)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListBackups returned %d, want 2", len(got))
	}
	if got[0].Status.Phase != velerov1.BackupPhaseCompleted {
		t.Errorf("expected first backup phase Completed, got %q", got[0].Status.Phase)
	}
}

func TestGetBackupReturnsNamed(t *testing.T) {
	b := velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "bk"},
		Spec: velerov1.BackupSpec{
			IncludedNamespaces: []string{"ns1", "ns2"},
			StorageLocation:    "default",
		},
		Status: velerov1.BackupStatus{Phase: velerov1.BackupPhaseCompleted, Warnings: 1},
	}
	vc := &fakeVeleroClient{backups: []velerov1.Backup{b}}

	got, err := core.GetBackup(context.Background(), vc, "bk")
	if err != nil {
		t.Fatalf("GetBackup: %v", err)
	}
	if got.Name != "bk" || len(got.Spec.IncludedNamespaces) != 2 || got.Status.Warnings != 1 {
		t.Errorf("unexpected backup %+v", got)
	}
}

func TestDeleteBackupForwardsName(t *testing.T) {
	vc := &fakeVeleroClient{}
	if err := core.DeleteBackup(context.Background(), vc, "gone"); err != nil {
		t.Fatalf("DeleteBackup: %v", err)
	}
	if vc.deletedBackup != "gone" {
		t.Errorf("deletedBackup = %q, want gone", vc.deletedBackup)
	}
}

func TestCreateRestoreDefaultNameAndForward(t *testing.T) {
	vc := &fakeVeleroClient{}
	name := core.DefaultRestoreName("bk1", fixedNow())
	spec := velerov1.RestoreSpec{BackupName: "bk1"}
	if err := core.CreateRestore(context.Background(), vc, spec, name, false); err != nil {
		t.Fatalf("CreateRestore: %v", err)
	}
	if vc.createdRestoreName != "horizon-restore-bk1-20260612-143005" {
		t.Errorf("createdRestoreName = %q", vc.createdRestoreName)
	}
	if vc.createdRestoreSpec.BackupName != "bk1" {
		t.Errorf("createdRestoreSpec.BackupName = %q, want bk1", vc.createdRestoreSpec.BackupName)
	}
}

func TestListRestoresAndGetRestore(t *testing.T) {
	r := velerov1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "r1"},
		Spec: velerov1.RestoreSpec{
			BackupName:         "bk1",
			IncludedNamespaces: []string{"ns1"},
			NamespaceMapping:   map[string]string{"old": "new"},
		},
		Status: velerov1.RestoreStatus{Phase: velerov1.RestorePhaseCompleted, Warnings: 2, Errors: 1},
	}
	vc := &fakeVeleroClient{restores: []velerov1.Restore{r}}

	list, err := core.ListRestores(context.Background(), vc)
	if err != nil {
		t.Fatalf("ListRestores: %v", err)
	}
	if len(list) != 1 || list[0].Spec.BackupName != "bk1" {
		t.Errorf("unexpected restores %+v", list)
	}

	got, err := core.GetRestore(context.Background(), vc, "r1")
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	if got.Spec.NamespaceMapping["old"] != "new" || got.Status.Phase != velerov1.RestorePhaseCompleted {
		t.Errorf("unexpected restore %+v", got)
	}
}
