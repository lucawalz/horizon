package tui

import (
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func TestFmtNamespaceMapping(t *testing.T) {
	if got := fmtNamespaceMapping(nil); got != "-" {
		t.Errorf("nil mapping = %q, want -", got)
	}
	got := fmtNamespaceMapping(map[string]string{"b": "y", "a": "x"})
	if got != "a:x,b:y" {
		t.Errorf("mapping = %q, want a:x,b:y (sorted)", got)
	}
}
