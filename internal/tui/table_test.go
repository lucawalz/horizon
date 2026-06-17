package tui

import (
	"strings"
	"testing"
)

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
