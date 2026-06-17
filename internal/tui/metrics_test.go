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

func TestMetricsShownInWideDashboard(t *testing.T) {
	m := newMetricsModel(230, 56)
	out := stripStyling(m.wideDashboard())
	for _, want := range []string{"Metrics", "Workload", "GitOps"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide dashboard missing %q", want)
		}
	}
	if strings.Contains(out, "Node health") {
		t.Error("metrics panel should no longer show Node health")
	}
}

func TestDashboardHiddenWhenNotLoaded(t *testing.T) {
	m := newMetricsModel(230, 56)
	m.loaded = false
	if strings.Contains(stripStyling(m.dashboardBand()), "Metrics") {
		t.Error("dashboard should not render Metrics before the snapshot loads")
	}
}

func TestMetricsPanelStacksInMediumLayout(t *testing.T) {
	m := newMetricsModel(mediumBreakpoint+5, 60)
	if !strings.Contains(m.mediumDashboard(), "Metrics") {
		t.Error("medium dashboard missing stacked Metrics panel")
	}
}
