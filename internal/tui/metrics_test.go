package tui

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/core"
)

func metricsSnapshot() core.Snapshot {
	return core.Snapshot{
		Pressure: core.PressureSummary{CPUScore: 0.07, MemScore: 0.22},
		Nodes: []core.NodeRow{
			{Name: "master", Role: "master", Status: "Ready", IPv4: "10.20.0.10"},
			{Name: "worker-1", Role: "worker", Status: "Ready", IPv4: "10.20.0.11"},
		},
		Pools:      []core.PoolRow{{Name: "reserved-workers", Type: "reserved", Desired: "3", Ready: "3"}},
		Nudge:      core.NudgeState{Kind: core.NudgeInitialized},
		Autoscaler: core.AutoscalerState{Activity: "Running"},
		Workload:   core.WorkloadSummary{Running: 110, Deployments: core.WorkloadKind{Ready: 20, Desired: 20}},
		NodeHealth: core.NodeHealthSummary{CPURequests: 2000, CPUAlloc: 8000, MemRequests: 4 << 30, MemAlloc: 16 << 30},
		Flux:       core.FluxSummary{Kustomizations: core.FluxKind{Ready: 12, Total: 12}, HelmReleases: core.FluxKind{Ready: 18, Total: 18}},
	}
}

func newMetricsModel(width, height int) model {
	m := newModel(&core.App{Cluster: "burst"})
	m.loading = false
	m.loaded = true
	m.snap = metricsSnapshot()
	m.width, m.height = width, height
	m.relayout()
	return m
}

func TestMetricsAsideShownWideBesideLog(t *testing.T) {
	m := newMetricsModel(230, 56)
	if w := m.metricsAsideWidth(m.logHeight(m.headerBand(), m.layoutDashboard(m.headerBand(), m.inputBand()), m.inputBand())); w <= 0 {
		t.Fatalf("expected metrics aside width > 0 in wide layout, got %d", w)
	}
	out := m.View()
	for _, want := range []string{"Metrics", "Workload", "Node health", "GitOps"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide view missing %q", want)
		}
	}
}

func TestMetricsAsideHiddenWhenNotLoaded(t *testing.T) {
	m := newMetricsModel(230, 56)
	m.loaded = false
	if w := m.metricsAsideWidth(40); w != 0 {
		t.Errorf("aside width = %d, want 0 when not loaded", w)
	}
}

func TestMetricsAsideHiddenBelowWideBreakpoint(t *testing.T) {
	m := newMetricsModel(mediumBreakpoint+1, 56)
	if w := m.metricsAsideWidth(40); w != 0 {
		t.Errorf("aside width = %d, want 0 below wide breakpoint", w)
	}
}

func TestMetricsPanelStacksInMediumLayout(t *testing.T) {
	m := newMetricsModel(mediumBreakpoint+5, 60)
	if !strings.Contains(m.mediumDashboard(), "Metrics") {
		t.Error("medium dashboard missing stacked Metrics panel")
	}
}
