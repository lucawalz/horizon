package cli_test

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildRestoreSpec(t *testing.T) {
	cmd := cli.NewRestoreCmdForTest(newTestApp())
	create, _, err := cmd.Find([]string{"create"})
	if err != nil {
		t.Fatalf("find create: %v", err)
	}
	if err := create.ParseFlags([]string{
		"--from-backup", "bk1",
		"--include-namespaces", "ns1",
		"--namespace-mappings", "old:new",
	}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	spec, err := cli.BuildRestoreSpecForTest(create)
	if err != nil {
		t.Fatalf("BuildRestoreSpec: %v", err)
	}
	if spec.BackupName != "bk1" {
		t.Errorf("BackupName = %q", spec.BackupName)
	}
	if spec.NamespaceMapping["old"] != "new" {
		t.Errorf("NamespaceMapping = %v", spec.NamespaceMapping)
	}
}

func TestBuildRestoreSpec_BadMapping(t *testing.T) {
	cmd := cli.NewRestoreCmdForTest(newTestApp())
	create, _, _ := cmd.Find([]string{"create"})
	if err := create.ParseFlags([]string{"--from-backup", "bk", "--namespace-mappings", "noseparator"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := cli.BuildRestoreSpecForTest(create); err == nil {
		t.Fatal("expected error from malformed mapping")
	}
}

func TestRestoreCreate_MissingFromBackup(t *testing.T) {
	fake := &fakeVeleroClient{}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewRestoreCmdForTest(newTestApp())
	cmd.SetArgs([]string{"create"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --from-backup absent")
	}
}

func TestRestoreCreate_DefaultName(t *testing.T) {
	restoreNow := cli.SetNowFuncForTest(fixedNow)
	defer restoreNow()
	fake := &fakeVeleroClient{}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewRestoreCmdForTest(newTestApp())
	cmd.SetArgs([]string{"create", "--from-backup", "bk1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.createdRestoreName != "horizon-restore-bk1-20260612-143005" {
		t.Errorf("createdRestoreName = %q", fake.createdRestoreName)
	}
}

func TestRestoreList(t *testing.T) {
	r := velerov1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "r1"},
		Spec:       velerov1.RestoreSpec{BackupName: "bk1"},
		Status:     velerov1.RestoreStatus{Phase: velerov1.RestorePhaseCompleted, Warnings: 2, Errors: 1},
	}
	fake := &fakeVeleroClient{restores: []velerov1.Restore{r}}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewRestoreCmdForTest(newTestApp())
	cmd.SetArgs([]string{"list"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
	if strings.Contains(out, "EXPIRES") {
		t.Errorf("restore list must not have EXPIRES column:\n%s", out)
	}
	for _, want := range []string{"NAME", "BACKUP", "r1", "bk1", "Completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreDescribe(t *testing.T) {
	r := velerov1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "r1"},
		Spec: velerov1.RestoreSpec{
			BackupName:         "bk1",
			IncludedNamespaces: []string{"ns1"},
			NamespaceMapping:   map[string]string{"old": "new"},
		},
		Status: velerov1.RestoreStatus{Phase: velerov1.RestorePhaseInProgress},
	}
	fake := &fakeVeleroClient{restores: []velerov1.Restore{r}}
	restoreVC := cli.SetVeleroClientForTest(fake)
	defer restoreVC()

	cmd := cli.NewRestoreCmdForTest(newTestApp())
	cmd.SetArgs([]string{"describe", "r1"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
	for _, want := range []string{"Backup:", "bk1", "ns1", "old:new", "InProgress"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe missing %q:\n%s", want, out)
		}
	}
}
