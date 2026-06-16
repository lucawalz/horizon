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
	if err := core.CreateBackup(context.Background(), vc, spec, "my-backup", false, core.Progress{}); err != nil {
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
	if err := core.CreateBackup(context.Background(), vc, spec, "wb", true, core.Progress{}); err != nil {
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
	if err := core.DeleteBackup(context.Background(), vc, "gone", core.Progress{}); err != nil {
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
	if err := core.CreateRestore(context.Background(), vc, spec, name, false, core.Progress{}); err != nil {
		t.Fatalf("CreateRestore: %v", err)
	}
	if vc.createdRestoreName != "horizon-restore-bk1-20260612-143005" {
		t.Errorf("createdRestoreName = %q", vc.createdRestoreName)
	}
	if vc.createdRestoreSpec.BackupName != "bk1" {
		t.Errorf("createdRestoreSpec.BackupName = %q, want bk1", vc.createdRestoreSpec.BackupName)
	}
}

func TestCreateScheduleForwardsSpecAndName(t *testing.T) {
	vc := &fakeVeleroClient{}
	spec := velerov1.ScheduleSpec{
		Schedule: "0 3 * * *",
		Template: velerov1.BackupSpec{IncludedNamespaces: []string{"app"}},
	}
	if err := core.CreateSchedule(context.Background(), vc, spec, "nightly", core.Progress{}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if vc.createdScheduleName != "nightly" {
		t.Errorf("createdScheduleName = %q, want nightly", vc.createdScheduleName)
	}
	if vc.createdScheduleSpec.Schedule != "0 3 * * *" {
		t.Errorf("Schedule = %q, want 0 3 * * *", vc.createdScheduleSpec.Schedule)
	}
	if len(vc.createdScheduleSpec.Template.IncludedNamespaces) != 1 {
		t.Errorf("template namespaces = %v", vc.createdScheduleSpec.Template.IncludedNamespaces)
	}
}

func TestListSchedulesAndGetSchedule(t *testing.T) {
	s := velerov1.Schedule{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly"},
		Spec:       velerov1.ScheduleSpec{Schedule: "0 3 * * *"},
		Status:     velerov1.ScheduleStatus{Phase: velerov1.SchedulePhaseEnabled},
	}
	vc := &fakeVeleroClient{schedules: []velerov1.Schedule{s}}

	list, err := core.ListSchedules(context.Background(), vc)
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(list) != 1 || list[0].Spec.Schedule != "0 3 * * *" {
		t.Errorf("unexpected schedules %+v", list)
	}

	got, err := core.GetSchedule(context.Background(), vc, "nightly")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.Status.Phase != velerov1.SchedulePhaseEnabled {
		t.Errorf("phase = %q, want Enabled", got.Status.Phase)
	}
}

func TestDeleteScheduleForwardsName(t *testing.T) {
	vc := &fakeVeleroClient{}
	if err := core.DeleteSchedule(context.Background(), vc, "nightly", core.Progress{}); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if vc.deletedSchedule != "nightly" {
		t.Errorf("deletedSchedule = %q, want nightly", vc.deletedSchedule)
	}
}

func TestListAndCreateStorageLocations(t *testing.T) {
	bsl := velerov1.BackupStorageLocation{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: velerov1.BackupStorageLocationSpec{
			Provider:    "aws",
			StorageType: velerov1.StorageType{ObjectStorage: &velerov1.ObjectStorageLocation{Bucket: "b"}},
		},
	}
	vc := &fakeVeleroClient{locations: []velerov1.BackupStorageLocation{bsl}}

	list, err := core.ListBackupStorageLocations(context.Background(), vc)
	if err != nil {
		t.Fatalf("ListBackupStorageLocations: %v", err)
	}
	if len(list) != 1 || list[0].Spec.Provider != "aws" {
		t.Errorf("unexpected locations %+v", list)
	}

	spec := velerov1.BackupStorageLocationSpec{Provider: "aws"}
	if err := core.CreateBackupStorageLocation(context.Background(), vc, spec, "secondary", core.Progress{}); err != nil {
		t.Fatalf("CreateBackupStorageLocation: %v", err)
	}
	if vc.createdBSLName != "secondary" || vc.createdBSLSpec.Provider != "aws" {
		t.Errorf("created BSL = %q/%q", vc.createdBSLName, vc.createdBSLSpec.Provider)
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
