package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/cli"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func fixedNow() time.Time {
	return time.Date(2026, 6, 12, 14, 30, 5, 0, time.UTC)
}

func TestBuildBackupSpec(t *testing.T) {
	cmd := cli.NewBackupCmdForTest(newTestApp())
	create, _, err := cmd.Find([]string{"create"})
	if err != nil {
		t.Fatalf("find create: %v", err)
	}
	if err := create.ParseFlags([]string{
		"--include-namespaces", "a,b",
		"--include-resources", "deployments",
		"--ttl", "168h",
		"--selector", "app=web",
	}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	spec, err := cli.BuildBackupSpecForTest(create)
	if err != nil {
		t.Fatalf("BuildBackupSpec: %v", err)
	}
	if len(spec.IncludedNamespaces) != 2 || spec.IncludedNamespaces[0] != "a" {
		t.Errorf("IncludedNamespaces = %v", spec.IncludedNamespaces)
	}
	if len(spec.IncludedResources) != 1 || spec.IncludedResources[0] != "deployments" {
		t.Errorf("IncludedResources = %v", spec.IncludedResources)
	}
	if spec.TTL != (metav1.Duration{Duration: 168 * time.Hour}) {
		t.Errorf("TTL = %v, want 168h", spec.TTL)
	}
	if spec.SnapshotVolumes == nil || !*spec.SnapshotVolumes {
		t.Errorf("SnapshotVolumes = %v, want &true", spec.SnapshotVolumes)
	}
	if spec.LabelSelector == nil || spec.LabelSelector.MatchLabels["app"] != "web" {
		t.Errorf("LabelSelector = %v, want app=web", spec.LabelSelector)
	}
}

func TestBuildBackupSpec_BadSelector(t *testing.T) {
	cmd := cli.NewBackupCmdForTest(newTestApp())
	create, _, _ := cmd.Find([]string{"create"})
	if err := create.ParseFlags([]string{"--selector", "=="}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := cli.BuildBackupSpecForTest(create); err == nil {
		t.Fatal("expected error from malformed selector")
	}
}

func TestDefaultBackupName(t *testing.T) {
	restore := cli.SetNowFuncForTest(fixedNow)
	defer restore()

	if got := cli.DefaultBackupNameForTest([]string{"sentio-systems"}); got != "horizon-sentio-systems-20260612-143005" {
		t.Errorf("name = %q", got)
	}
	if got := cli.DefaultBackupNameForTest(nil); got != "horizon-all-20260612-143005" {
		t.Errorf("name = %q", got)
	}
}

func TestBackupCreate_NameHonored(t *testing.T) {
	fake := &fakeVeleroClient{}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewBackupCmdForTest(newTestApp())
	cmd.SetArgs([]string{"create", "--include-namespaces", "ns1", "--name", "my-backup"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.createdBackupName != "my-backup" {
		t.Errorf("createdBackupName = %q, want my-backup", fake.createdBackupName)
	}
	if fake.waited {
		t.Error("create without --wait must not trigger")
	}
}

func TestBackupCreate_WaitTriggers(t *testing.T) {
	fake := &fakeVeleroClient{}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewBackupCmdForTest(newTestApp())
	cmd.SetArgs([]string{"create", "--include-namespaces", "ns1", "--name", "wb", "--wait"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !fake.waited {
		t.Error("create --wait must call TriggerBackup")
	}
}

func TestBackupList(t *testing.T) {
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
	fake := &fakeVeleroClient{backups: []velerov1.Backup{older, newer}}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewBackupCmdForTest(newTestApp())
	cmd.SetArgs([]string{"list"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})

	if !strings.Contains(out, "NAME") || !strings.Contains(out, "EXPIRES") {
		t.Errorf("missing header:\n%s", out)
	}
	if strings.Index(out, "new-backup") > strings.Index(out, "old-backup") {
		t.Errorf("backups not sorted newest-first:\n%s", out)
	}
	if !strings.Contains(out, "Completed") {
		t.Errorf("missing phase:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[len(lines)-1], "-") {
		t.Errorf("nil-timestamp row should render dash:\n%s", out)
	}
}

func TestBackupDescribe(t *testing.T) {
	b := velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "bk"},
		Spec: velerov1.BackupSpec{
			IncludedNamespaces: []string{"ns1", "ns2"},
			StorageLocation:    "default",
		},
		Status: velerov1.BackupStatus{Phase: velerov1.BackupPhaseCompleted, Errors: 0, Warnings: 1},
	}
	fake := &fakeVeleroClient{backups: []velerov1.Backup{b}}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewBackupCmdForTest(newTestApp())
	cmd.SetArgs([]string{"describe", "bk"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
	for _, want := range []string{"Name:", "bk", "ns1,ns2", "Completed", "Warnings:"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe missing %q:\n%s", want, out)
		}
	}
}

func TestBackupDelete(t *testing.T) {
	fake := &fakeVeleroClient{}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewBackupCmdForTest(newTestApp())
	cmd.SetArgs([]string{"delete", "gone"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
	if fake.deletedBackup != "gone" {
		t.Errorf("deletedBackup = %q, want gone", fake.deletedBackup)
	}
	if !strings.Contains(out, "delete request submitted") {
		t.Errorf("missing delete notice:\n%s", out)
	}
}
