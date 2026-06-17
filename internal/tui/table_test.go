package tui

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
)

func TestTableNaturalWidth(t *testing.T) {
	headers := []string{"NAME", "PHASE", "CP-INITIALIZED"}
	rows := [][]string{{"burst", "Provisioned", "true"}}
	want := (5 + cellPadding) + (11 + cellPadding) + (14 + cellPadding)
	if got := tableNaturalWidth(headers, rows); got != want {
		t.Errorf("tableNaturalWidth = %d, want %d", got, want)
	}
}

func TestWideSplitShrinksRightGrowsLeft(t *testing.T) {
	left, right := wideSplit(180, 38, 4)
	if right != 42 {
		t.Errorf("right = %d, want 42 (content-sized)", right)
	}
	if left != 138 {
		t.Errorf("left = %d, want 138 (takes the rest)", left)
	}
	if left <= right {
		t.Errorf("left %d should exceed right %d", left, right)
	}
	if _, narrowRight := wideSplit(100, 38, 4); narrowRight > 100*2/5 {
		t.Errorf("right %d exceeds the 2/5 cap on a narrow wide terminal", narrowRight)
	}
	if _, tinyRight := wideSplit(200, 4, 2); tinyRight < minRightColWidth {
		t.Errorf("right %d below floor %d", tinyRight, minRightColWidth)
	}
}

func lineContainsBoth(s, a, b string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, a) && strings.Contains(ln, b) {
			return true
		}
	}
	return false
}

func TestWideDashboardKeepsNodeRowsIntact(t *testing.T) {
	m := testModel()
	m.loaded = true
	m.width = 200
	m.snap = core.Snapshot{Nodes: []core.NodeRow{
		{Name: "reserved-worker-kl8px", Role: "worker", PodCount: 6, Status: "Ready", IPv4: "100.110.21.71"},
		{Name: "reserved-worker-m5znj", Role: "worker", PodCount: 6, Status: "Ready", IPv4: "100.71.115.99"},
		{Name: "worker-2", Role: "worker", CPUPercent: 7, MemPercent: 36, MetricsPresent: true, PodCount: 42, Status: "Ready", IPv4: "10.20.0.12"},
	}}
	out := stripStyling(m.wideDashboard())
	for _, ln := range strings.Split(out, "\n") {
		if w := len([]rune(strings.TrimRight(ln, " "))); w > m.width {
			t.Errorf("dashboard line width %d exceeds terminal %d:\n%q", w, m.width, ln)
		}
	}
	for _, n := range [][2]string{
		{"reserved-worker-kl8px", "100.110.21.71"},
		{"reserved-worker-m5znj", "100.71.115.99"},
	} {
		if !lineContainsBoth(out, n[0], n[1]) {
			t.Errorf("node %q and its IP %q are not on the same line (wrapped):\n%s", n[0], n[1], out)
		}
	}
}

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
