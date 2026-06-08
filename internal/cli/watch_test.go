package cli_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRunSinglePollCycle_HealsStrandCreatedMidSession(t *testing.T) {
	ns := "sentio-systems"
	burstAff := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key:      k8s.NodeAffinityLabelKey,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{ns},
				}},
			}},
		},
	}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: burstAff}}},
	}
	burstNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "horizon-burst-aaaa", Labels: map[string]string{k8s.NodeAffinityLabelKey: ns}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}

	kc := fake.NewSimpleClientset(dep, burstNode)
	deps := cli.WatchDepsForTest{KubeClient: kc, MetricPusher: &mockPusher{}}
	state := cli.WatchRuntimeState{}
	ctx := context.Background()

	if err := cli.RunSinglePollCycleWithWorkloadForTest(ctx, deps, ns, &state); err != nil {
		t.Fatalf("first cycle: %v", err)
	}
	d, _ := kc.AppsV1().Deployments(ns).Get(ctx, "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity == nil || d.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		t.Fatal("affinity should remain while burst node is present")
	}

	if err := kc.CoreV1().Nodes().Delete(ctx, "horizon-burst-aaaa", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete node: %v", err)
	}

	if err := cli.RunSinglePollCycleWithWorkloadForTest(ctx, deps, ns, &state); err != nil {
		t.Fatalf("second cycle: %v", err)
	}
	d, _ = kc.AppsV1().Deployments(ns).Get(ctx, "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity != nil && d.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		t.Error("strand created mid-session was not healed on a later poll cycle")
	}
}

type mockPusher struct {
	calls   int
	lastErr error
	metrics []map[string]float64
}

func (m *mockPusher) PushContext(_ context.Context) error {
	m.calls++
	return m.lastErr
}

func TestPressureScore_Formula(t *testing.T) {
	cases := []struct {
		cpu     float64
		mem     float64
		pending int
		want    float64
	}{
		{0.5, 0.3, 0, 0.5},
		{0.5, 0.3, 2, 0.7},
		{0.9, 0.3, 5, 1.0},
		{0.0, 0.0, 0, 0.0},
		{0.85, 0.95, 0, 0.95},
	}
	for _, c := range cases {
		got := cli.ComputePressureScoreForTest(c.cpu, c.mem, c.pending)
		if got != c.want {
			t.Errorf("ComputePressureScoreForTest(%v, %v, %d) = %v, want %v", c.cpu, c.mem, c.pending, got, c.want)
		}
	}
}

func TestPressureWindow_MovingAverage(t *testing.T) {
	w := cli.NewPressureWindowForTest(5)

	if avg := w.Average(); avg != 0.0 {
		t.Errorf("empty window Average() = %v, want 0.0", avg)
	}

	v1, v2 := 0.1, 0.2
	w.Add(v1)
	w.Add(v2)
	want2 := (v1 + v2) / 2.0
	if avg := w.Average(); avg != want2 {
		t.Errorf("2-sample average = %v, want %v", avg, want2)
	}

	w.Add(0.3)
	w.Add(0.4)
	w.Add(0.5)
	avg := w.Average()
	want := (0.1 + 0.2 + 0.3 + 0.4 + 0.5) / 5
	if avg != want {
		t.Errorf("5-sample average = %v, want %v", avg, want)
	}

	w.Add(1.0)
	avg = w.Average()
	want = (0.2 + 0.3 + 0.4 + 0.5 + 1.0) / 5
	if avg != want {
		t.Errorf("eviction average = %v, want %v", avg, want)
	}
}

func TestHysteresis_3SamplesAboveBeforeTrigger(t *testing.T) {
	threshold := 0.80

	state := cli.WatchRuntimeState{PressureCount: 0}

	_, count := cli.EvaluateHysteresisForTest(&state, 0.85, threshold)
	if count != 1 {
		t.Errorf("after 1 above-threshold: count = %d, want 1", count)
	}
	state.PressureCount = count

	_, count = cli.EvaluateHysteresisForTest(&state, 0.85, threshold)
	if count != 2 {
		t.Errorf("after 2 above-threshold: count = %d, want 2", count)
	}
	state.PressureCount = count

	burst, count := cli.EvaluateHysteresisForTest(&state, 0.90, threshold)
	if count != 3 {
		t.Errorf("after 3 above-threshold: count = %d, want 3", count)
	}
	if !burst {
		t.Error("burst should fire after 3 consecutive above-threshold samples")
	}

	state2 := cli.WatchRuntimeState{PressureCount: 0}
	cli.EvaluateHysteresisForTest(&state2, 0.90, threshold)
	state2.PressureCount = 1

	_, count2 := cli.EvaluateHysteresisForTest(&state2, 0.50, threshold)
	if count2 != 0 {
		t.Errorf("below-threshold resets count: got %d, want 0", count2)
	}
	state2.PressureCount = count2

	cli.EvaluateHysteresisForTest(&state2, 0.85, threshold)
	state2.PressureCount = 1
	cli.EvaluateHysteresisForTest(&state2, 0.85, threshold)
	state2.PressureCount = 2

	burst2, _ := cli.EvaluateHysteresisForTest(&state2, 0.90, threshold)
	if !burst2 {
		t.Error("burst should fire after reset and 3 new consecutive above-threshold")
	}
}

func TestCooldown_BlocksBurstWhileActive(t *testing.T) {
	active := cli.WatchRuntimeState{CooldownUntil: time.Now().Add(5 * time.Minute)}
	if cli.ShouldEvaluatePressureForTest(active, time.Now()) {
		t.Error("ShouldEvaluatePressure must return false while cooldown is active")
	}

	expired := cli.WatchRuntimeState{CooldownUntil: time.Now().Add(-1 * time.Minute)}
	if !cli.ShouldEvaluatePressureForTest(expired, time.Now()) {
		t.Error("ShouldEvaluatePressure must return true when cooldown has expired")
	}

	zero := cli.WatchRuntimeState{}
	if !cli.ShouldEvaluatePressureForTest(zero, time.Now()) {
		t.Error("ShouldEvaluatePressure must return true when CooldownUntil is zero")
	}
}

func TestCooldown_PreventsBurstViaDecideScaleAction(t *testing.T) {
	state := cli.WatchRuntimeState{
		CooldownUntil:  time.Now().Add(5 * time.Minute),
		PressureCount:  5,
		ActiveBurstIDs: []string{"x"},
	}
	thresholds := config.ThresholdConfig{
		Burst:           0.80,
		ScaleDown:       0.40,
		Window:          5,
		CooldownMinutes: 10,
		MaxBurstNodes:   3,
	}

	gateOpen := cli.ShouldEvaluatePressureForTest(state, time.Now())
	action := cli.DecideScaleActionForTest(state, 0.95, thresholds)
	if gateOpen {
		t.Fatalf("gate should be closed while CooldownUntil is in the future")
	}
	_ = action

	state2 := cli.WatchRuntimeState{
		CooldownUntil:  time.Now().Add(-1 * time.Minute),
		PressureCount:  0,
		ActiveBurstIDs: []string{"x"},
	}
	if !cli.ShouldEvaluatePressureForTest(state2, time.Now()) {
		t.Error("gate should be open after cooldown expires")
	}
	if cli.DecideScaleActionForTest(state2, 0.95, thresholds) != cli.ScaleNone {
		t.Error("DecideScaleAction must return ScaleNone when PressureCount=0 (hysteresis must rebuild)")
	}
}

func TestScaleOut_OneBurstPerCycle(t *testing.T) {
	thresholds := config.ThresholdConfig{Burst: 0.80, MaxBurstNodes: 3}

	empty := cli.WatchRuntimeState{ActiveBurstIDs: []string{}}
	if cli.DecideScaleActionForTest(empty, 0.95, thresholds) != cli.ScaleOut {
		t.Error("expected ScaleOut when ActiveBurstIDs is empty and score above threshold")
	}

	one := cli.WatchRuntimeState{ActiveBurstIDs: []string{"a"}, PressureCount: 3}
	if cli.DecideScaleActionForTest(one, 0.95, thresholds) != cli.ScaleOut {
		t.Error("expected ScaleOut when 1 active and max is 3")
	}

	full := cli.WatchRuntimeState{ActiveBurstIDs: []string{"a", "b", "c"}, PressureCount: 3}
	if cli.DecideScaleActionForTest(full, 0.95, thresholds) != cli.ScaleNone {
		t.Error("expected ScaleNone when at max_burst_nodes")
	}
}

func TestScaleIn_RemovesOldestFirst(t *testing.T) {
	state := cli.WatchRuntimeState{ActiveBurstIDs: []string{"oldest", "middle", "newest"}}
	victim := cli.SelectVictimForScaleInForTest(state)
	if victim != "oldest" {
		t.Errorf("victim = %q, want %q", victim, "oldest")
	}

	empty := cli.WatchRuntimeState{ActiveBurstIDs: []string{}}
	if cli.SelectVictimForScaleInForTest(empty) != "" {
		t.Error("empty ActiveBurstIDs must return empty victim")
	}
}

func TestReconcileActiveBurstIDs_PrunesPhantom(t *testing.T) {
	got := cli.ReconcileActiveBurstIDsForTest(
		[]string{"phantom"},
		map[string]bool{},
		map[string]bool{},
	)
	if len(got) != 0 {
		t.Errorf("phantom id (exited, no node) must be pruned, got %v", got)
	}
}

func TestReconcileActiveBurstIDs_KeepsInFlight(t *testing.T) {
	got := cli.ReconcileActiveBurstIDsForTest(
		[]string{"booting"},
		map[string]bool{"booting": true},
		map[string]bool{},
	)
	if len(got) != 1 || got[0] != "booting" {
		t.Errorf("in-flight id (no node yet) must be kept, got %v", got)
	}
}

func TestReconcileActiveBurstIDs_KeepsLiveNode(t *testing.T) {
	got := cli.ReconcileActiveBurstIDsForTest(
		[]string{"healthy"},
		map[string]bool{},
		map[string]bool{"healthy": true},
	)
	if len(got) != 1 || got[0] != "healthy" {
		t.Errorf("successful id (live node, subprocess exited) must be kept, got %v", got)
	}
}

func TestMetricPush_CalledEveryCycle(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	mp := &mockPusher{}
	deps := cli.WatchDepsForTest{
		KubeClient:    kc,
		MetricPusher:  mp,
	}

	state := cli.WatchRuntimeState{}
	for i := 0; i < 3; i++ {
		if err := cli.RunSinglePollCycleForTest(ctx, deps, &state); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
	}
	if mp.calls != 3 {
		t.Errorf("pusher calls = %d, want 3", mp.calls)
	}
}

func TestPidFilePath_UsesBurstID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	path, err := cli.PidFilePathForTest("a3f2")
	if err != nil {
		t.Fatalf("PidFilePathForTest: %v", err)
	}

	want := filepath.Join(dir, "horizon", "a3f2.pid")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestPidFilePath_RejectsInvalidBurstID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	_, err := cli.PidFilePathForTest("../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path-traversal burst_id")
	}
	if !containsStr(err.Error(), "burst_id") {
		t.Errorf("error %q must contain \"burst_id\"", err.Error())
	}
}

func TestRunWatch_Shutdown_OnSIGTERM(t *testing.T) {
	kc := fake.NewSimpleClientset()
	mp := &mockPusher{}
	deps := cli.WatchDepsForTest{
		KubeClient:   kc,
		MetricPusher: mp,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	app := &cli.App{Config: &config.Config{}}

	done := make(chan error, 1)
	go func() {
		done <- cli.RunWatchForTest(ctx, app, deps, "test")
	}()

	select {
	case err := <-done:
		if err != nil && err != context.DeadlineExceeded {
			t.Errorf("RunWatchForTest returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWatchForTest did not return within 5s after context cancel")
	}

	if mp.calls < 1 {
		t.Errorf("pusher calls = %d, want >= 1", mp.calls)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}


