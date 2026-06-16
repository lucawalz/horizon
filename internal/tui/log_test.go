package tui

import (
	"strings"
	"testing"
)

func filledLog(width, height, lines int) logModel {
	l := newLog(width, height)
	for i := 0; i < lines; i++ {
		l.append(strings.Repeat("x", 4) + itoa(i))
	}
	return l
}

func TestViewDoesNotResetScroll(t *testing.T) {
	m := testModel()
	m.width = 120
	m.height = 40
	m.loaded = true
	m.relayout()
	for i := 0; i < 200; i++ {
		m.log.append("line " + itoa(i))
	}
	m.log.view.GotoTop()
	want := m.log.view.YOffset
	_ = m.View()
	_ = m.View()
	if got := m.log.view.YOffset; got != want {
		t.Fatalf("View mutated scroll offset: got %d, want %d", got, want)
	}
}

func TestAppendAutoFollowsOnlyAtBottom(t *testing.T) {
	l := filledLog(40, 5, 50)
	if !l.view.AtBottom() {
		t.Fatal("expected log to start at bottom after appends")
	}
	l.view.GotoTop()
	top := l.view.YOffset
	l.append("more")
	if l.view.YOffset != top {
		t.Fatalf("scrolled-up log should stay put, offset moved from %d to %d", top, l.view.YOffset)
	}
	l.view.GotoBottom()
	l.append("tail")
	if !l.view.AtBottom() {
		t.Fatal("log at bottom should follow new output")
	}
}

func TestScrollLabelReportsPosition(t *testing.T) {
	l := filledLog(40, 5, 50)
	if got := l.scrollLabel(); got != "end" {
		t.Fatalf("label at bottom = %q, want %q", got, "end")
	}
	l.view.GotoTop()
	if got := l.scrollLabel(); got != "top" {
		t.Fatalf("label at top = %q, want %q", got, "top")
	}
}

func TestLogFloorGivesUsableHeight(t *testing.T) {
	m := testModel()
	m.height = 40
	if got := m.logFloor(); got <= minLogHeight && got < int(float64(m.height)*logHeightShare) {
		t.Fatalf("logFloor too small on a 40-row terminal: %d", got)
	}
}
