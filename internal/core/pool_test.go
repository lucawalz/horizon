package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
)

func TestScaleUpCreatesReservedServers(t *testing.T) {
	p := newFakeProvider()

	target := reservedTarget(2)
	if err := core.ScaleUp(context.Background(), p, target, false, core.Progress{}); err != nil {
		t.Fatalf("ScaleUp: %v", err)
	}

	if got := reservedCount(t, p); got != 2 {
		t.Errorf("reserved instances = %d, want 2", got)
	}
	if calls := p.CreateCalls(); len(calls) != 2 {
		t.Errorf("create calls = %d, want 2", len(calls))
	}
}

func TestScaleUpNoOpWhenAlreadyAtTarget(t *testing.T) {
	p := newFakeProvider("reserved-a", "reserved-b")

	var msgs []string
	target := reservedTarget(2)
	if err := core.ScaleUp(context.Background(), p, target, false, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleUp: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "nothing to do") {
		t.Errorf("expected no-op message, got %v", msgs)
	}
}

func TestScaleUpDryRunDoesNotMutate(t *testing.T) {
	p := newFakeProvider()

	var msgs []string
	target := reservedTarget(3)
	if err := core.ScaleUp(context.Background(), p, target, true, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleUp dry-run: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "0 -> 3") {
		t.Errorf("dry-run progress missing delta: %v", msgs)
	}
	if got := reservedCount(t, p); got != 0 {
		t.Errorf("dry-run must not create servers, got %d", got)
	}
}

func TestScaleDownDeletesAllReservedServers(t *testing.T) {
	p := newFakeProvider("reserved-a", "reserved-b")

	target := reservedTarget(0)
	if err := core.ScaleDown(context.Background(), p, target, false, core.Progress{}); err != nil {
		t.Fatalf("ScaleDown: %v", err)
	}
	if got := reservedCount(t, p); got != 0 {
		t.Errorf("servers after scale-down = %d, want 0", got)
	}
}

func TestScaleDownDryRunDoesNotMutate(t *testing.T) {
	p := newFakeProvider("reserved-a")

	var msgs []string
	target := reservedTarget(0)
	if err := core.ScaleDown(context.Background(), p, target, true, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleDown dry-run: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "1 -> 0") {
		t.Errorf("dry-run progress missing intent: %v", msgs)
	}
	if got := reservedCount(t, p); got != 1 {
		t.Errorf("dry-run must not delete servers, got %d", got)
	}
}
