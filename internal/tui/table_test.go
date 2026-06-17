package tui

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
)

func nonEmptyLines(s string) []string {
	out := []string{}
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

func TestNodesBodyFitsWidthWithoutWrapping(t *testing.T) {
	snap := core.Snapshot{Nodes: []core.NodeRow{
		{Name: "master", Role: "master", CPUPercent: 22, MemPercent: 21, MetricsPresent: true, PodCount: 22, Status: "Ready", IPv4: "10.20.0.10"},
		{Name: "reserved-worker-kl8px", Role: "worker", PodCount: 6, Status: "Ready", IPv4: "100.110.21.71"},
		{Name: "reserved-worker-m5znj", Role: "worker", PodCount: 6, Status: "Ready", IPv4: "100.71.115.99"},
		{Name: "reserved-worker-xrbjd", Role: "worker", PodCount: 6, Status: "Ready", IPv4: "100.100.28.1"},
		{Name: "worker-1", Role: "worker", CPUPercent: 6, MemPercent: 7, MetricsPresent: true, PodCount: 24, Status: "Ready", IPv4: "10.20.0.11"},
	}}
	for _, inner := range []int{58, 65, 72, 90, 120} {
		out := nodesBody(snap, inner, true)
		lines := nonEmptyLines(stripStyling(out))
		if len(lines) != 1+len(snap.Nodes) {
			t.Errorf("inner=%d: got %d non-empty lines, want %d (a row wrapped):\n%s", inner, len(lines), 1+len(snap.Nodes), out)
		}
		for _, ln := range lines {
			if w := len([]rune(strings.TrimRight(ln, " "))); w > inner {
				t.Errorf("inner=%d: rendered line width %d exceeds inner %d:\n%q", inner, w, inner, ln)
			}
		}
	}
}

func TestRenderLogTableEmpty(t *testing.T) {
	if got := renderLogTable(nil); got != "" {
		t.Errorf("empty table = %q, want empty", got)
	}
	if got := renderLogTable([][]string{}); got != "" {
		t.Errorf("zero-row table = %q, want empty", got)
	}
}

func TestRenderLogTableAlignsColumns(t *testing.T) {
	rows := [][]string{
		{"NAME", "STATUS"},
		{"a", "Ready"},
		{"longer-name", "NotReady"},
	}
	out := renderLogTable(rows)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	idx := strings.Index(stripStyling(lines[1]), "Ready")
	idx2 := strings.Index(stripStyling(lines[2]), "NotReady")
	if idx != idx2 {
		t.Errorf("second column not aligned: %d vs %d", idx, idx2)
	}
	if !strings.Contains(stripStyling(lines[2]), "longer-name") {
		t.Errorf("data row missing content:\n%s", out)
	}
}

func TestFitNameColumnReservesHeaderWidth(t *testing.T) {
	headers := []string{"NAME", "PODS", "IP"}
	rows := [][]string{{"reserved-worker-mxz8j", "6", "100.118.194.110"}}
	inner := 36
	fitNameColumn(headers, rows, 0, inner)
	nameBudget := inner - (len("PODS") + cellPadding) - (len("100.118.194.110") + cellPadding) - cellPadding
	if got := len([]rune(rows[0][0])); got > nameBudget {
		t.Errorf("name width %d exceeds header-aware budget %d; row overflows inner", got, nameBudget)
	}
}

func stripStyling(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}
