package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/core"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errTest = errors.New("boom")

func TestPhaseOrDash(t *testing.T) {
	if got := phaseOrDash(""); got != "-" {
		t.Errorf("empty phase = %q, want -", got)
	}
	if got := phaseOrDash("Completed"); got != "Completed" {
		t.Errorf("phase = %q, want Completed", got)
	}
}

func TestJoinOrDash(t *testing.T) {
	if got := joinOrDash(nil); got != "-" {
		t.Errorf("nil = %q, want -", got)
	}
	if got := joinOrDash([]string{"ns1", "ns2"}); got != "ns1,ns2" {
		t.Errorf("join = %q, want ns1,ns2", got)
	}
}

func TestFmtTime(t *testing.T) {
	if got := fmtTime(nil); got != "-" {
		t.Errorf("nil time = %q, want -", got)
	}
	var zero metav1.Time
	if got := fmtTime(&zero); got != "-" {
		t.Errorf("zero time = %q, want -", got)
	}
	ts := metav1.NewTime(time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC))
	if got := fmtTime(&ts); got != ts.Format(core.BackupTimeLayout) {
		t.Errorf("time = %q, want %q", got, ts.Format(core.BackupTimeLayout))
	}
}

func TestFmtBool(t *testing.T) {
	if fmtBool(true) != "true" {
		t.Errorf("fmtBool(true) = %q", fmtBool(true))
	}
	if fmtBool(false) != "false" {
		t.Errorf("fmtBool(false) = %q", fmtBool(false))
	}
}

func TestOnSchedulesLoadedRendersRows(t *testing.T) {
	m := testModel()
	schedule := velerov1.Schedule{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly"},
		Spec:       velerov1.ScheduleSpec{Schedule: "0 3 * * *", Paused: true},
		Status:     velerov1.ScheduleStatus{Phase: velerov1.SchedulePhaseEnabled},
	}
	updated, _ := m.onSchedulesLoaded(schedulesLoadedMsg{schedules: []velerov1.Schedule{schedule}})
	out := strings.Join(updated.(model).log.lines, "\n")
	for _, want := range []string{"NAME", "SCHEDULE", "nightly", "0 3 * * *", "Enabled", "true"} {
		if !strings.Contains(out, want) {
			t.Errorf("schedule list output missing %q:\n%s", want, out)
		}
	}
}

func TestOnSchedulesLoadedError(t *testing.T) {
	m := testModel()
	updated, _ := m.onSchedulesLoaded(schedulesLoadedMsg{err: errTest})
	out := strings.Join(updated.(model).log.lines, "\n")
	if !strings.Contains(out, "boom") {
		t.Errorf("expected error rendered, got:\n%s", out)
	}
}

func TestOnStorageLocationsLoadedRendersRows(t *testing.T) {
	m := testModel()
	bsl := velerov1.BackupStorageLocation{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: velerov1.BackupStorageLocationSpec{
			Provider:    "aws",
			Default:     true,
			StorageType: velerov1.StorageType{ObjectStorage: &velerov1.ObjectStorageLocation{Bucket: "horizon-backups"}},
		},
		Status: velerov1.BackupStorageLocationStatus{Phase: velerov1.BackupStorageLocationPhaseAvailable},
	}
	updated, _ := m.onStorageLocationsLoaded(storageLocationsLoadedMsg{locations: []velerov1.BackupStorageLocation{bsl}})
	out := strings.Join(updated.(model).log.lines, "\n")
	for _, want := range []string{"NAME", "PROVIDER", "BUCKET", "default", "aws", "horizon-backups", "Available", "true"} {
		if !strings.Contains(out, want) {
			t.Errorf("bsl list output missing %q:\n%s", want, out)
		}
	}
}

func TestFmtNamespaceMapping(t *testing.T) {
	if got := fmtNamespaceMapping(nil); got != "-" {
		t.Errorf("nil mapping = %q, want -", got)
	}
	got := fmtNamespaceMapping(map[string]string{"b": "y", "a": "x"})
	if got != "a:x,b:y" {
		t.Errorf("mapping = %q, want a:x,b:y (sorted)", got)
	}
}
