package tui

import (
	"strings"
	"testing"
)

func TestRenderTableEmpty(t *testing.T) {
	if got := renderTable(nil); got != "" {
		t.Errorf("empty table = %q, want empty", got)
	}
	if got := renderTable([][]string{}); got != "" {
		t.Errorf("zero-row table = %q, want empty", got)
	}
}

func TestRenderTableAlignsColumns(t *testing.T) {
	rows := [][]string{
		{"NAME", "STATUS"},
		{"a", "Ready"},
		{"longer-name", "NotReady"},
	}
	out := renderTable(rows)
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

func TestPad(t *testing.T) {
	if got := pad("ab", 5); got != "ab   " {
		t.Errorf("pad = %q, want %q", got, "ab   ")
	}
	if got := pad("toolong", 3); got != "toolong" {
		t.Errorf("pad must not truncate, got %q", got)
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
