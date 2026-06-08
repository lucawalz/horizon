package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/prometheus/common/model"
	"k8s.io/client-go/kubernetes"
)

const (
	hysteresisRequiredSamples = 3
	pendingPodPenalty         = 0.2
	pressureScoreCeiling      = 1.0
	defaultMaxBurstNodes      = 1
	pushJobName               = "horizon-watch"
	metricBurstActive         = "horizon_burst_active"
	metricBurstNodeCount      = "horizon_burst_node_count"
	metricLastBurstDuration   = "horizon_last_burst_duration_seconds"
	metricPressureScore       = "horizon_pressure_score"
)

type ScaleAction int

const (
	ScaleNone ScaleAction = iota
	ScaleOut
	ScaleIn
)

type WatchRuntimeState struct {
	PressureCount  int
	CooldownUntil  time.Time
	ActiveBurstIDs []string
	Window         *PressureWindow
	LastBurstStart time.Time
}

type PressureWindow struct {
	samples []float64
	head    int
	count   int
}

type metricPusher interface {
	PushContext(ctx context.Context) error
}

type pusherFactory func(snapshot watchMetricsSnapshot) metricPusher

type watchMetricsSnapshot struct {
	BurstActive       float64
	BurstNodeCount    float64
	LastBurstDuration float64
	PressureScore     float64
}

type promQuerier interface {
	QueryInstant(ctx context.Context, query string) (model.Vector, error)
}

type watchDeps struct {
	kc          kubernetes.Interface
	prom        promQuerier
	pushFactory pusherFactory
	cfg         *config.Config
	workload    string
}

type WatchDepsForTest struct {
	KubeClient   kubernetes.Interface
	MetricPusher metricPusher
}

func computePressureScore(cpu, mem float64, pendingPods int) float64 {
	base := cpu
	if mem > base {
		base = mem
	}
	if pendingPods > 0 {
		base += pendingPodPenalty
	}
	if base > pressureScoreCeiling {
		return pressureScoreCeiling
	}
	if base < 0 {
		return 0
	}
	return base
}

func newPressureWindow(size int) *PressureWindow {
	if size <= 0 {
		size = 1
	}
	return &PressureWindow{samples: make([]float64, size)}
}

func (w *PressureWindow) Add(v float64) {
	w.samples[w.head%len(w.samples)] = v
	w.head++
	if w.count < len(w.samples) {
		w.count++
	}
}

func (w *PressureWindow) Average() float64 {
	if w.count == 0 {
		return 0
	}
	sum := 0.0
	for i := 0; i < w.count; i++ {
		sum += w.samples[i]
	}
	return sum / float64(w.count)
}

func evaluateHysteresis(state *WatchRuntimeState, sample, threshold float64) (bool, int) {
	if sample >= threshold {
		state.PressureCount++
	} else {
		state.PressureCount = 0
	}
	return state.PressureCount >= hysteresisRequiredSamples, state.PressureCount
}

func shouldEvaluatePressure(state WatchRuntimeState, now time.Time) bool {
	if state.CooldownUntil.IsZero() {
		return true
	}
	return now.After(state.CooldownUntil) || now.Equal(state.CooldownUntil)
}

func decideScaleAction(state WatchRuntimeState, score float64, t config.ThresholdConfig) ScaleAction {
	maxNodes := t.MaxBurstNodes
	if maxNodes <= 0 {
		maxNodes = defaultMaxBurstNodes
	}
	activeCount := len(state.ActiveBurstIDs)
	hysteresisMet := activeCount == 0 || state.PressureCount >= hysteresisRequiredSamples
	if score >= t.Burst && hysteresisMet && activeCount < maxNodes {
		return ScaleOut
	}
	if score < t.ScaleDown && activeCount > 0 {
		return ScaleIn
	}
	return ScaleNone
}

func selectVictimForScaleIn(state WatchRuntimeState) string {
	if len(state.ActiveBurstIDs) == 0 {
		return ""
	}
	return state.ActiveBurstIDs[0]
}

func defaultPusherFactory(url string) pusherFactory {
	return func(snap watchMetricsSnapshot) metricPusher {
		reg := prometheus.NewRegistry()
		for name, val := range map[string]float64{
			metricBurstActive:       snap.BurstActive,
			metricBurstNodeCount:    snap.BurstNodeCount,
			metricLastBurstDuration: snap.LastBurstDuration,
			metricPressureScore:     snap.PressureScore,
		} {
			g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name})
			g.Set(val)
			reg.MustRegister(g)
		}
		return push.New(url, pushJobName).Gatherer(reg)
	}
}

func buildSnapshot(state WatchRuntimeState, score float64, now time.Time) watchMetricsSnapshot {
	snap := watchMetricsSnapshot{
		BurstNodeCount: float64(len(state.ActiveBurstIDs)),
		PressureScore:  score,
	}
	if len(state.ActiveBurstIDs) > 0 {
		snap.BurstActive = 1
	}
	if !state.LastBurstStart.IsZero() {
		snap.LastBurstDuration = now.Sub(state.LastBurstStart).Seconds()
	}
	return snap
}

const (
	cpuPressureQuery     = `1 - avg by (instance)(rate(node_cpu_seconds_total{mode="idle"}[5m]))`
	memPressureQuery     = `1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
	pendingPressureQuery = `count(kube_pod_status_phase{phase="Pending"}==1) or vector(0)`
)

func runSinglePollCycle(ctx context.Context, deps *watchDeps, state *WatchRuntimeState) error {
	cpuVec, _ := deps.prom.QueryInstant(ctx, cpuPressureQuery)
	memVec, _ := deps.prom.QueryInstant(ctx, memPressureQuery)
	pendVec, _ := deps.prom.QueryInstant(ctx, pendingPressureQuery)

	cpu := vectorAverage(cpuVec)
	mem := vectorAverage(memVec)
	pending := 0
	if len(pendVec) > 0 {
		pending = int(pendVec[0].Value)
	}

	sample := computePressureScore(cpu, mem, pending)
	if state.Window == nil {
		state.Window = newPressureWindow(deps.cfg.Thresholds.Window)
	}
	state.Window.Add(sample)
	avg := state.Window.Average()

	now := time.Now()
	if shouldEvaluatePressure(*state, now) {
		evaluateHysteresis(state, avg, deps.cfg.Thresholds.Burst)
	}

	snap := buildSnapshot(*state, avg, now)
	if deps.pushFactory != nil {
		if err := deps.pushFactory(snap).PushContext(ctx); err != nil {
			return fmt.Errorf("watch: push: %w", err)
		}
	}

	if err := persistWatchState(ctx, deps.kc, *state); err != nil {
		return fmt.Errorf("watch: persist state: %w", err)
	}
	return nil
}

func vectorAverage(vec model.Vector) float64 {
	if len(vec) == 0 {
		return 0
	}
	var sum float64
	for _, s := range vec {
		sum += float64(s.Value)
	}
	return sum / float64(len(vec))
}

func persistWatchState(ctx context.Context, kc kubernetes.Interface, s WatchRuntimeState) error {
	return k8s.WriteWatchState(ctx, kc, k8s.WatchState{
		PressureCount:  s.PressureCount,
		CooldownUntil:  s.CooldownUntil,
		ActiveBurstIDs: s.ActiveBurstIDs,
	})
}

func RunSinglePollCycleForTest(ctx context.Context, deps WatchDepsForTest, state *WatchRuntimeState) error {
	now := time.Now()
	var avg float64
	if state.Window != nil {
		avg = state.Window.Average()
	}
	snap := buildSnapshot(*state, avg, now)
	if deps.MetricPusher != nil {
		if err := deps.MetricPusher.PushContext(ctx); err != nil {
			return fmt.Errorf("watch: push: %w", err)
		}
	}
	_ = snap
	if deps.KubeClient != nil {
		if err := persistWatchState(ctx, deps.KubeClient, *state); err != nil {
			return fmt.Errorf("watch: persist state: %w", err)
		}
	}
	return nil
}

func RunWatchForTest(ctx context.Context, app *App, deps WatchDepsForTest, workload string) error {
	return fmt.Errorf("watch: RunWatchForTest not yet implemented")
}

func PidFilePathForTest(burstID string) (string, error) {
	return PidFilePath(burstID)
}

func ComputePressureScoreForTest(cpu, mem float64, pending int) float64 {
	return computePressureScore(cpu, mem, pending)
}

func NewPressureWindowForTest(size int) *PressureWindow { return newPressureWindow(size) }

func EvaluateHysteresisForTest(state *WatchRuntimeState, sample, threshold float64) (bool, int) {
	return evaluateHysteresis(state, sample, threshold)
}

func ShouldEvaluatePressureForTest(state WatchRuntimeState, now time.Time) bool {
	return shouldEvaluatePressure(state, now)
}

func DecideScaleActionForTest(state WatchRuntimeState, score float64, t config.ThresholdConfig) ScaleAction {
	return decideScaleAction(state, score, t)
}

func SelectVictimForScaleInForTest(state WatchRuntimeState) string {
	return selectVictimForScaleIn(state)
}

func NewWatchDepsForTest(kc kubernetes.Interface, prom promQuerier, factory pusherFactory, cfg *config.Config, workload string) *watchDeps {
	return &watchDeps{kc: kc, prom: prom, pushFactory: factory, cfg: cfg, workload: workload}
}
