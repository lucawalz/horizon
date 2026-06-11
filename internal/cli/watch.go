package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	hzprom "github.com/lucawalz/horizon/internal/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/prometheus/common/model"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const defaultPushgatewayURL = "http://kube-prometheus-stack-pushgateway.monitoring.svc:9091"

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

const (
	pollInterval        = 30 * time.Second
	scaleOutCooldown    = 2 * pollInterval
	shutdownGracePeriod = 3 * time.Minute
	waitPollInterval    = 100 * time.Millisecond
	nodeBurstLabel      = "horizon.dev/burst=true"
	burstHostnamePrefix = "horizon-burst-"
	burstSubcommand     = "burst"
	burstWorkloadFlag   = "--workload"
	downSubcommand      = "down"
	burstIDFlag         = "--burst-id"
	burstIDByteLen      = 4
	burstFailureBackoff = 5 * time.Minute
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
	if t.Burst > 0 && score >= t.Burst && hysteresisMet && activeCount < maxNodes {
		return ScaleOut
	}
	if score < t.ScaleDown && activeCount > 0 {
		return ScaleIn
	}
	return ScaleNone
}

func performScaleIn(ctx context.Context, mgr *subprocessManager, state *WatchRuntimeState, t config.ThresholdConfig) {
	victim := selectVictimForScaleIn(*state)
	if victim == "" {
		return
	}
	cooldown := func() {
		state.CooldownUntil = time.Now().Add(time.Duration(t.CooldownMinutes) * time.Minute)
		state.PressureCount = 0
	}
	if mgr.inFlightBurstIDs()[victim] {
		_ = mgr.signalAndWait(victim, syscall.SIGTERM, shutdownGracePeriod)
		cooldown()
		return
	}
	if err := mgr.teardown(ctx, victim); err != nil {
		fmt.Fprintf(os.Stderr, "watch: teardown %s: %v\n", victim, err)
		return
	}
	cooldown()
}

func recordScaleOut(state *WatchRuntimeState, burstID string, now time.Time) {
	state.ActiveBurstIDs = append(state.ActiveBurstIDs, burstID)
	state.LastBurstStart = now
	state.CooldownUntil = now.Add(scaleOutCooldown)
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
	cpuPressureQuery = `1 - avg by (instance)(rate(node_cpu_seconds_total{mode="idle"}[5m]))`
	memPressureQuery = `1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
)

func pendingPressureQuery(namespace string) string {
	return fmt.Sprintf(`count(kube_pod_status_phase{phase="Pending",namespace!=%q}==1) or vector(0)`, namespace)
}

func runSinglePollCycle(ctx context.Context, deps *watchDeps, state *WatchRuntimeState) error {
	var cpu, mem float64
	pending := 0
	if deps.prom != nil {
		cpuVec, _ := deps.prom.QueryInstant(ctx, cpuPressureQuery)
		memVec, _ := deps.prom.QueryInstant(ctx, memPressureQuery)
		pendVec, _ := deps.prom.QueryInstant(ctx, pendingPressureQuery(deps.workload))
		if deps.kc != nil {
			excludeHosts, err := burstNodeInternalIPs(ctx, deps.kc)
			if err != nil {
				fmt.Fprintf(os.Stderr, "watch: list burst node IPs: %v\n", err)
				excludeHosts = map[string]bool{}
			}
			cpu = vectorAverageExcludingHosts(cpuVec, excludeHosts)
			mem = vectorAverageExcludingHosts(memVec, excludeHosts)
		} else {
			cpu = vectorAverage(cpuVec)
			mem = vectorAverage(memVec)
		}
		if len(pendVec) > 0 {
			pending = int(pendVec[0].Value)
		}
	}

	sample := computePressureScore(cpu, mem, pending)
	if state.Window == nil {
		size := 0
		if deps.cfg != nil {
			size = deps.cfg.Thresholds.Window
		}
		state.Window = newPressureWindow(size)
	}
	state.Window.Add(sample)
	avg := state.Window.Average()

	now := time.Now()
	if deps.cfg != nil && shouldEvaluatePressure(*state, now) {
		evaluateHysteresis(state, avg, deps.cfg.Thresholds.Burst)
	}

	if deps.kc != nil && deps.workload != "" {
		if err := k8s.ReconcileStrandedAffinity(ctx, deps.kc, deps.workload); err != nil {
			fmt.Fprintf(os.Stderr, "watch: reconcile stranded affinity: %v\n", err)
		}
	}

	if deps.kc != nil {
		if err := persistWatchState(ctx, deps.kc, *state); err != nil {
			return fmt.Errorf("watch: persist state: %w", err)
		}
	}

	if deps.pushFactory != nil {
		if p := deps.pushFactory(buildSnapshot(*state, avg, now)); p != nil {
			if err := p.PushContext(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "watch: metrics push failed (non-fatal): %v\n", err)
			}
		}
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

func vectorAverageExcludingHosts(vec model.Vector, excludeHosts map[string]bool) float64 {
	var sum float64
	count := 0
	for _, s := range vec {
		instance := string(s.Metric["instance"])
		host := instance
		if i := strings.IndexByte(instance, ':'); i >= 0 {
			host = instance[:i]
		}
		if excludeHosts[host] {
			continue
		}
		sum += float64(s.Value)
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func persistWatchState(ctx context.Context, kc kubernetes.Interface, s WatchRuntimeState) error {
	return k8s.WriteWatchState(ctx, kc, k8s.WatchState{
		PressureCount:  s.PressureCount,
		CooldownUntil:  s.CooldownUntil,
		ActiveBurstIDs: s.ActiveBurstIDs,
	})
}

func constPusherFactory(p metricPusher) pusherFactory {
	return func(watchMetricsSnapshot) metricPusher { return p }
}

func (d WatchDepsForTest) toWatchDeps(cfg *config.Config, workload string) *watchDeps {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &watchDeps{
		kc:          d.KubeClient,
		pushFactory: constPusherFactory(d.MetricPusher),
		cfg:         cfg,
		workload:    workload,
	}
}

func RunSinglePollCycleForTest(ctx context.Context, deps WatchDepsForTest, state *WatchRuntimeState) error {
	return runSinglePollCycle(ctx, deps.toWatchDeps(nil, ""), state)
}

func RunSinglePollCycleWithWorkloadForTest(ctx context.Context, deps WatchDepsForTest, workload string, state *WatchRuntimeState) error {
	return runSinglePollCycle(ctx, deps.toWatchDeps(nil, workload), state)
}

type managedBurst struct {
	cmd *exec.Cmd
	pid int
}

type subprocessManager struct {
	mu          sync.Mutex
	bursts      map[string]*managedBurst
	stateDir    string
	teardownFn  func(ctx context.Context, burstID string) error
	lastFailure time.Time
	terminating map[string]bool
}

func newSubprocessManager(stateDir string) *subprocessManager {
	m := &subprocessManager{
		bursts:      make(map[string]*managedBurst),
		stateDir:    stateDir,
		terminating: make(map[string]bool),
	}
	m.teardownFn = m.spawnDown
	return m
}

func gracefulCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = shutdownGracePeriod
	return cmd
}

func burstSpawnArgs(workload, burstID string) []string {
	return []string{burstSubcommand, burstWorkloadFlag, workload, burstIDFlag, burstID}
}

func (m *subprocessManager) spawn(ctx context.Context, workload, burstID string) error {
	if !burstIDPattern.MatchString(burstID) {
		return fmt.Errorf("watch: invalid burst_id %q", burstID)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("watch: resolve executable: %w", err)
	}
	cmd := gracefulCommandContext(ctx, self, burstSpawnArgs(workload, burstID)...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("watch: spawn burst: %w", err)
	}
	pid := cmd.Process.Pid
	pidPath, err := PidFilePath(burstID)
	if err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("watch: pid path: %w", err)
	}
	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("watch: state dir: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("watch: write pid file: %w", err)
	}
	m.mu.Lock()
	m.bursts[burstID] = &managedBurst{cmd: cmd, pid: pid}
	m.mu.Unlock()

	go func() {
		err := cmd.Wait()
		m.recordBurstExit(burstID, err)
		_ = os.Remove(pidPath)
	}()
	return nil
}

func (m *subprocessManager) recordBurstExit(burstID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil && !m.terminating[burstID] {
		m.lastFailure = time.Now()
	}
	delete(m.terminating, burstID)
	delete(m.bursts, burstID)
}

func (m *subprocessManager) inFailureBackoff(now time.Time, window time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.lastFailure.IsZero() && now.Before(m.lastFailure.Add(window))
}

func (m *subprocessManager) teardown(ctx context.Context, burstID string) error {
	if !burstIDPattern.MatchString(burstID) {
		return fmt.Errorf("watch: invalid burst_id %q", burstID)
	}
	return m.teardownFn(ctx, burstID)
}

func (m *subprocessManager) spawnDown(ctx context.Context, burstID string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("watch: resolve executable: %w", err)
	}
	cmd := gracefulCommandContext(ctx, self, downSubcommand, burstIDFlag, burstID)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("watch: spawn down: %w", err)
	}
	m.mu.Lock()
	m.bursts[burstID] = &managedBurst{cmd: cmd, pid: cmd.Process.Pid}
	m.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		delete(m.bursts, burstID)
		m.mu.Unlock()
	}()
	return nil
}

func (m *subprocessManager) inFlightBurstIDs() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make(map[string]bool, len(m.bursts))
	for id := range m.bursts {
		ids[id] = true
	}
	return ids
}

func (m *subprocessManager) signalAll(sig syscall.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mb := range m.bursts {
		m.terminating[id] = true
		if mb.cmd != nil && mb.cmd.Process != nil {
			_ = mb.cmd.Process.Signal(sig)
		}
	}
}

func (m *subprocessManager) signalAndWait(burstID string, sig syscall.Signal, timeout time.Duration) error {
	m.mu.Lock()
	mb, ok := m.bursts[burstID]
	if ok {
		m.terminating[burstID] = true
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if mb.cmd != nil && mb.cmd.Process != nil {
		_ = mb.cmd.Process.Signal(sig)
	}
	done := make(chan struct{})
	go func() {
		m.waitFor(burstID)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		if mb.cmd != nil && mb.cmd.Process != nil {
			_ = mb.cmd.Process.Kill()
		}
		return fmt.Errorf("watch: subprocess %s did not exit within %s", burstID, timeout)
	}
}

func (m *subprocessManager) waitFor(burstID string) {
	for {
		m.mu.Lock()
		_, ok := m.bursts[burstID]
		m.mu.Unlock()
		if !ok {
			return
		}
		time.Sleep(waitPollInterval)
	}
}

func (m *subprocessManager) waitAll(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		for {
			m.mu.Lock()
			n := len(m.bursts)
			m.mu.Unlock()
			if n == 0 {
				close(done)
				return
			}
			time.Sleep(waitPollInterval)
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func liveBurstNodeIDsOrdered(ctx context.Context, kc kubernetes.Interface) ([]string, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: nodeBurstLabel})
	if err != nil {
		return nil, err
	}
	items := make([]corev1.Node, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		if strings.HasPrefix(n.Name, burstHostnamePrefix) {
			items = append(items, n)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ti, tj := items[i].CreationTimestamp.Time, items[j].CreationTimestamp.Time
		if ti.Equal(tj) {
			return items[i].Name < items[j].Name
		}
		return ti.Before(tj)
	})
	ids := make([]string, 0, len(items))
	for _, n := range items {
		ids = append(ids, strings.TrimPrefix(n.Name, burstHostnamePrefix))
	}
	return ids, nil
}

func burstNodeInternalIPs(ctx context.Context, kc kubernetes.Interface) (map[string]bool, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: nodeBurstLabel})
	if err != nil {
		return nil, err
	}
	ips := make(map[string]bool)
	for _, n := range nodes.Items {
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				ips[addr.Address] = true
			}
		}
	}
	return ips, nil
}

func adoptActiveBursts(ctx context.Context, kc kubernetes.Interface, ws k8s.WatchState) []string {
	live, err := liveBurstNodeIDsOrdered(ctx, kc)
	if err != nil {
		return ws.ActiveBurstIDs
	}
	return reconcileActiveBurstIDs(ws.ActiveBurstIDs, nil, live)
}

func reconcileActiveState(ctx context.Context, kc kubernetes.Interface, mgr *subprocessManager, state *WatchRuntimeState) {
	if kc == nil {
		return
	}
	liveNodes, err := liveBurstNodeIDsOrdered(ctx, kc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: reconcile active bursts: list nodes: %v\n", err)
		return
	}
	state.ActiveBurstIDs = reconcileActiveBurstIDs(state.ActiveBurstIDs, mgr.inFlightBurstIDs(), liveNodes)
	if err := persistWatchState(ctx, kc, *state); err != nil {
		fmt.Fprintf(os.Stderr, "watch: reconcile active bursts: persist: %v\n", err)
	}
}

func reconcileActiveBurstIDs(prior []string, inFlight map[string]bool, liveNodesOrdered []string) []string {
	live := make(map[string]bool, len(liveNodesOrdered))
	for _, id := range liveNodesOrdered {
		live[id] = true
	}
	active := make([]string, 0, len(liveNodesOrdered)+len(inFlight))
	active = append(active, liveNodesOrdered...)
	seen := make(map[string]bool, len(liveNodesOrdered))
	for _, id := range liveNodesOrdered {
		seen[id] = true
	}
	for _, id := range prior {
		if seen[id] || live[id] {
			continue
		}
		if inFlight[id] {
			active = append(active, id)
			seen[id] = true
		}
	}
	for id := range inFlight {
		if !seen[id] && !live[id] {
			active = append(active, id)
			seen[id] = true
		}
	}
	return active
}

func newRandomBurstID() (string, error) {
	buf := make([]byte, burstIDByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("watch: rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func runWatch(parent context.Context, deps *watchDeps) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := deps.cfg
	if cfg == nil {
		cfg = &config.Config{}
		deps.cfg = cfg
	}
	workload := deps.workload

	var ws k8s.WatchState
	if deps.kc != nil {
		read, err := k8s.ReadWatchState(ctx, deps.kc)
		if err != nil {
			return fmt.Errorf("watch: read state: %w", err)
		}
		ws = read
		ws.ActiveBurstIDs = adoptActiveBursts(ctx, deps.kc, ws)
	}

	state := WatchRuntimeState{
		PressureCount:  0,
		CooldownUntil:  ws.CooldownUntil,
		ActiveBurstIDs: ws.ActiveBurstIDs,
		Window:         newPressureWindow(cfg.Thresholds.Window),
	}

	stateDir, err := stateDirOrTestOverride()
	if err != nil {
		return fmt.Errorf("watch: state dir: %w", err)
	}
	mgr := newSubprocessManager(stateDir)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if err := runSinglePollCycle(ctx, deps, &state); err != nil {
			fmt.Fprintf(os.Stderr, "watch: poll error: %v\n", err)
		}

		reconcileActiveState(ctx, deps.kc, mgr, &state)

		if shouldEvaluatePressure(state, time.Now()) {
			avg := 0.0
			if state.Window != nil {
				avg = state.Window.Average()
			}
			switch decideScaleAction(state, avg, cfg.Thresholds) {
			case ScaleOut:
				if mgr.inFailureBackoff(time.Now(), burstFailureBackoff) {
					fmt.Fprintf(os.Stderr, "watch: scale-out suppressed: recent burst failure\n")
					break
				}
				newID, err := newRandomBurstID()
				if err != nil {
					fmt.Fprintf(os.Stderr, "watch: burst id: %v\n", err)
				} else if err := mgr.spawn(ctx, workload, newID); err != nil {
					fmt.Fprintf(os.Stderr, "watch: spawn: %v\n", err)
				} else {
					recordScaleOut(&state, newID, time.Now())
				}
			case ScaleIn:
				performScaleIn(ctx, mgr, &state, cfg.Thresholds)
			}
		}

		select {
		case <-ctx.Done():
			mgr.signalAll(syscall.SIGTERM)
			mgr.waitAll(shutdownGracePeriod)
			_ = runSinglePollCycle(context.Background(), deps, &state)
			return nil
		case <-ticker.C:
		}
	}
}

func RunWatchForTest(ctx context.Context, app *App, deps WatchDepsForTest, workload string) error {
	var cfg *config.Config
	if app != nil {
		cfg = app.Config
	}
	return runWatch(ctx, deps.toWatchDeps(cfg, workload))
}

func GracefulCommandContextForTest(ctx context.Context, name string, args ...string) *exec.Cmd {
	return gracefulCommandContext(ctx, name, args...)
}

func BurstSpawnArgsForTest(workload, burstID string) []string {
	return burstSpawnArgs(workload, burstID)
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

func ReconcileActiveBurstIDsForTest(prior []string, inFlight map[string]bool, liveNodesOrdered []string) []string {
	return reconcileActiveBurstIDs(prior, inFlight, liveNodesOrdered)
}

func PerformScaleInForTest(ctx context.Context, teardownFn func(context.Context, string) error, state *WatchRuntimeState, t config.ThresholdConfig) {
	mgr := newSubprocessManager("")
	mgr.teardownFn = teardownFn
	performScaleIn(ctx, mgr, state, t)
}

func ReconcileActiveStateForTest(ctx context.Context, kc kubernetes.Interface, inFlight []string, state *WatchRuntimeState) {
	mgr := newSubprocessManager("")
	for _, id := range inFlight {
		mgr.bursts[id] = &managedBurst{}
	}
	reconcileActiveState(ctx, kc, mgr, state)
}

func AdoptActiveBurstsForTest(ctx context.Context, kc kubernetes.Interface, ws k8s.WatchState) []string {
	return adoptActiveBursts(ctx, kc, ws)
}

func NewWatchDepsForTest(kc kubernetes.Interface, prom promQuerier, factory pusherFactory, cfg *config.Config, workload string) *watchDeps {
	return &watchDeps{kc: kc, prom: prom, pushFactory: factory, cfg: cfg, workload: workload}
}

func VectorAverageExcludingHostsForTest(vec model.Vector, excludeHosts map[string]bool) float64 {
	return vectorAverageExcludingHosts(vec, excludeHosts)
}

func BurstNodeInternalIPsForTest(ctx context.Context, kc kubernetes.Interface) (map[string]bool, error) {
	return burstNodeInternalIPs(ctx, kc)
}

func PendingPressureQueryForTest(namespace string) string {
	return pendingPressureQuery(namespace)
}

func RecordScaleOutForTest(state *WatchRuntimeState, burstID string, now time.Time) {
	recordScaleOut(state, burstID, now)
}

func NewSubprocessManagerForTest() *subprocessManager {
	return newSubprocessManager("")
}

func RecordBurstExitForTest(m *subprocessManager, burstID string, err error) {
	m.recordBurstExit(burstID, err)
}

func MarkTerminatingForTest(m *subprocessManager, burstID string) {
	m.mu.Lock()
	m.terminating[burstID] = true
	m.mu.Unlock()
}

func InFailureBackoffForTest(m *subprocessManager, now time.Time, window time.Duration) bool {
	return m.inFailureBackoff(now, window)
}

func newWatchCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Continuously monitor cluster pressure and scale burst nodes",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			workload, _ := cmd.Flags().GetString("workload")
			if workload == "" {
				return fmt.Errorf("watch: --workload is required")
			}
			if err := k8s.ValidateNamespace(workload); err != nil {
				return fmt.Errorf("watch: %w", err)
			}
			deps, err := newWatchDeps(app, workload)
			if err != nil {
				return fmt.Errorf("watch: init: %w", err)
			}
			return runWatch(cmd.Context(), deps)
		},
	}
	cmd.Flags().String("workload", "", "target namespace to monitor and burst (required)")
	return cmd
}

func newWatchDeps(app *App, workload string) (*watchDeps, error) {
	prom, err := hzprom.NewClient(app.KubeClient, app.Config.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("watch: prometheus: %w", err)
	}
	url := app.Config.PushgatewayURL
	if url == "" {
		url = defaultPushgatewayURL
	}
	return &watchDeps{
		kc:          app.KubeClient,
		prom:        prom,
		pushFactory: defaultPusherFactory(url),
		cfg:         app.Config,
		workload:    workload,
	}, nil
}

func NewWatchCmdForTest(app *App) *cobra.Command { return newWatchCmd(app) }
