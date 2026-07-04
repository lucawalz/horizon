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

	got, err := p.ListReservedServers(context.Background())
	if err != nil {
		t.Fatalf("ListReservedServers: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("reserved servers = %d, want 2", len(got))
	}
	if len(p.servers) != 2 {
		t.Errorf("created servers = %d, want 2", len(p.servers))
	}
}

func TestScaleUpRefusesElastic(t *testing.T) {
	p := newFakeProvider()

	target := core.PoolTarget{PoolType: core.ElasticPoolType, Replicas: 2}
	err := core.ScaleUp(context.Background(), p, target, false, core.Progress{})
	if err == nil || !strings.Contains(err.Error(), "elastic") {
		t.Fatalf("expected elastic refusal, got %v", err)
	}
}

func TestScaleUpNoOpWhenAlreadyAtTarget(t *testing.T) {
	p := newFakeProvider(reservedServer(1, "reserved-a"), reservedServer(2, "reserved-b"))

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
	if len(p.servers) != 0 {
		t.Errorf("dry-run must not create servers, got %d", len(p.servers))
	}
}

func TestScaleDownDeletesAllReservedServers(t *testing.T) {
	p := newFakeProvider(reservedServer(1, "reserved-a"), reservedServer(2, "reserved-b"))

	target := reservedTarget(0)
	if err := core.ScaleDown(context.Background(), p, target, false, core.Progress{}); err != nil {
		t.Fatalf("ScaleDown: %v", err)
	}
	if len(p.servers) != 0 {
		t.Errorf("servers after scale-down = %d, want 0", len(p.servers))
	}
}

func TestScaleDownRefusesElastic(t *testing.T) {
	p := newFakeProvider()

	target := core.PoolTarget{PoolType: core.ElasticPoolType}
	err := core.ScaleDown(context.Background(), p, target, false, core.Progress{})
	if err == nil || !strings.Contains(err.Error(), "elastic") {
		t.Fatalf("expected elastic refusal, got %v", err)
	}
}

func TestScaleDownDryRunDoesNotMutate(t *testing.T) {
	p := newFakeProvider(reservedServer(1, "reserved-a"))

	var msgs []string
	target := reservedTarget(0)
	if err := core.ScaleDown(context.Background(), p, target, true, collectProgress(&msgs)); err != nil {
		t.Fatalf("ScaleDown dry-run: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "1 -> 0") {
		t.Errorf("dry-run progress missing intent: %v", msgs)
	}
	if len(p.servers) != 1 {
		t.Errorf("dry-run must not delete servers, got %d", len(p.servers))
	}
}
