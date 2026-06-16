package core_test

import (
	"testing"

	"github.com/lucawalz/horizon/internal/core"
)

func TestProgressForwardsAndNoOps(t *testing.T) {
	var emitted, debugged []string
	p := core.NewProgress(
		func(s string) { emitted = append(emitted, s) },
		func(s string) { debugged = append(debugged, s) },
	)
	p.Emit("a")
	p.Debug("b")
	if len(emitted) != 1 || emitted[0] != "a" {
		t.Errorf("emit = %v", emitted)
	}
	if len(debugged) != 1 || debugged[0] != "b" {
		t.Errorf("debug = %v", debugged)
	}

	noDebug := core.NewProgress(func(s string) { emitted = append(emitted, s) }, nil)
	noDebug.Debug("ignored")
	if len(debugged) != 1 {
		t.Errorf("debug with nil sink should no-op, got %v", debugged)
	}

	zero := core.Progress{}
	zero.Emit("x")
	zero.Debug("y")
}
