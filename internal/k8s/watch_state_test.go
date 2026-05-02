package k8s_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestWatchState_RoundTrip(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	ws := k8s.WatchState{
		PressureCount:  2,
		CooldownUntil:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		ActiveBurstIDs: []string{"a3f2", "b8c1"},
	}

	if err := k8s.WriteWatchState(ctx, kc, ws); err != nil {
		t.Fatalf("WriteWatchState: %v", err)
	}

	got, err := k8s.ReadWatchState(ctx, kc)
	if err != nil {
		t.Fatalf("ReadWatchState: %v", err)
	}

	if got.PressureCount != 2 {
		t.Errorf("PressureCount: got %d, want 2", got.PressureCount)
	}
	if !got.CooldownUntil.Equal(ws.CooldownUntil) {
		t.Errorf("CooldownUntil: got %v, want %v", got.CooldownUntil, ws.CooldownUntil)
	}
	if !reflect.DeepEqual(got.ActiveBurstIDs, ws.ActiveBurstIDs) {
		t.Errorf("ActiveBurstIDs: got %v, want %v", got.ActiveBurstIDs, ws.ActiveBurstIDs)
	}
}

func TestWatchState_FallbackZero(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	got, err := k8s.ReadWatchState(ctx, kc)
	if err != nil {
		t.Fatalf("ReadWatchState: %v", err)
	}

	if got.PressureCount != 0 {
		t.Errorf("PressureCount: got %d, want 0", got.PressureCount)
	}
	if !got.CooldownUntil.IsZero() {
		t.Errorf("CooldownUntil: got %v, want zero", got.CooldownUntil)
	}
	if len(got.ActiveBurstIDs) != 0 {
		t.Errorf("ActiveBurstIDs: got %v, want empty", got.ActiveBurstIDs)
	}
}

func TestWatchState_CoexistsWithBurstPhase(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	if err := k8s.WriteBurstPhase(ctx, kc, k8s.BurstPhaseRunning); err != nil {
		t.Fatalf("WriteBurstPhase: %v", err)
	}

	if err := k8s.WriteWatchState(ctx, kc, k8s.WatchState{PressureCount: 1}); err != nil {
		t.Fatalf("WriteWatchState: %v", err)
	}

	phase := k8s.ReadBurstPhase(ctx, kc)
	if phase != k8s.BurstPhaseRunning {
		t.Errorf("BurstPhase: got %q, want Running", phase)
	}

	ws, err := k8s.ReadWatchState(ctx, kc)
	if err != nil {
		t.Fatalf("ReadWatchState: %v", err)
	}
	if ws.PressureCount != 1 {
		t.Errorf("PressureCount: got %d, want 1", ws.PressureCount)
	}
}

func TestWatchState_RFC3339CooldownFormat(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	ws := k8s.WatchState{
		CooldownUntil: time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC),
	}
	if err := k8s.WriteWatchState(ctx, kc, ws); err != nil {
		t.Fatalf("WriteWatchState: %v", err)
	}

	cm, err := kc.CoreV1().ConfigMaps("kube-system").Get(ctx, "horizon-state", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get ConfigMap: %v", err)
	}

	if got := cm.Data["cooldown_until"]; got != "2026-05-15T10:30:00Z" {
		t.Errorf("cooldown_until: got %q, want %q", got, "2026-05-15T10:30:00Z")
	}
	if got := cm.Data["pressure_count"]; got != "0" {
		t.Errorf("pressure_count: got %q, want %q", got, "0")
	}
	v := cm.Data["active_burst_ids"]
	if v != "null" && v != "[]" {
		t.Errorf("active_burst_ids: got %q, want \"null\" or \"[]\"", v)
	}
}
