package cli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type mockVelero struct {
	err   error
	calls []string
}

func (m *mockVelero) TriggerBackup(_ context.Context, ns, name string, _, _ time.Duration) error {
	m.calls = append(m.calls, ns+"/"+name)
	return m.err
}

func workloadPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestBurstDryRun(t *testing.T) {
	app := &cli.App{Config: &config.Config{Provider: "hetzner"}}
	out := captureStdout(func() {
		if err := cli.RunBurstDryRunForTest(app); err != nil {
			t.Errorf("RunBurstDryRunForTest: %v", err)
		}
	})
	for i := 1; i <= 7; i++ {
		want := fmt.Sprintf("[dry-run] Step %d:", i)
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Tailscale") {
		t.Errorf("dry-run output must not reference Tailscale (legacy):\n%s", out)
	}
	if !strings.Contains(out, "ZeroTier") {
		t.Errorf("dry-run output missing ZeroTier step:\n%s", out)
	}
	if !strings.Contains(out, "Migrate") {
		t.Errorf("dry-run output missing Migrate step:\n%s", out)
	}
	if !strings.Contains(out, "[dry-run] No actions executed.") {
		t.Errorf("missing trailing line:\n%s", out)
	}
}

func TestBurstDryRun_NoCloudCreds(t *testing.T) {
	origToken := os.Getenv("HCLOUD_TOKEN")
	os.Unsetenv("HCLOUD_TOKEN")
	defer os.Setenv("HCLOUD_TOKEN", origToken)

	app := &cli.App{Config: &config.Config{Provider: "hetzner"}}
	cmd := cli.NewBurstCmdForTest(app)
	cmd.SetArgs([]string{"--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("burst --dry-run must succeed without HCLOUD_TOKEN; got: %v", err)
	}
}

func TestBurstWorkloadFlag_Required(t *testing.T) {
	app := &cli.App{Config: &config.Config{Provider: "hetzner"}}
	cmd := cli.NewBurstCmdForTest(app)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --workload absent and --dry-run absent")
	}
	if !strings.Contains(err.Error(), "--workload") {
		t.Errorf("error %q does not mention --workload", err.Error())
	}
}

func TestBurstWorkloadFlag_InvalidNamespace(t *testing.T) {
	app := &cli.App{Config: &config.Config{Provider: "hetzner"}}
	cmd := cli.NewBurstCmdForTest(app)
	cmd.SetArgs([]string{"--workload", "Foo"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --workload fails namespace regex")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error %q does not mention namespace", err.Error())
	}
}

func TestBurstStepOrder(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-aabb1234"
	zt := &mockZeroTier{waitID: "member-99"}
	prov := &mockHetznerProvider{burstID: "aabb1234", hostname: hostname, serverID: "99"}
	kc := fake.NewSimpleClientset(
		readyNode(hostname),
		workloadPod("app1", "sentio-systems", hostname),
	)
	vc := &mockVelero{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	if err := cli.RunBurstForTest(context.Background(), newTestApp(), zt, prov, kc, vc, "sentio-systems"); err != nil {
		t.Fatalf("RunBurstForTest: %v", err)
	}
	if len(zt.authorizeCalls) != 1 || zt.authorizeCalls[0] != "member-99" {
		t.Errorf("authorize calls = %v, want [member-99]", zt.authorizeCalls)
	}
	if len(vc.calls) != 1 {
		t.Errorf("velero TriggerBackup calls = %v, want 1", vc.calls)
	}
	phase := k8s.ReadBurstPhase(context.Background(), kc)
	if phase != k8s.BurstPhaseRunning {
		t.Errorf("final BurstPhase = %q, want Running", phase)
	}
}

func TestBurstRollback_OnTerraformFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	tfErr := errors.New("terraform apply failed")
	zt := &mockZeroTier{waitID: "should-not-be-used"}
	prov := &mockHetznerProvider{burstID: "ccdd3344", hostname: "horizon-burst-ccdd3344", applyErr: tfErr}
	kc := fake.NewSimpleClientset()
	vc := &mockVelero{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunBurstForTest(context.Background(), newTestApp(), zt, prov, kc, vc, "sentio-systems")
	if err == nil {
		t.Fatal("expected error from terraform failure")
	}
	if len(zt.authorizeCalls) != 0 {
		t.Errorf("authorize must not run when terraform fails: %v", zt.authorizeCalls)
	}
	if prov.destroyCalls != 0 {
		t.Errorf("destroy must not run when terraform-apply itself failed: %v", prov.destroyCalls)
	}
	phase := k8s.ReadBurstPhase(context.Background(), kc)
	if phase != k8s.BurstPhaseIdle {
		t.Errorf("BurstPhase after rollback = %q, want Idle (post-run cleanup)", phase)
	}
}

func TestBurstSignalRollback(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	zt := &mockZeroTier{waitID: "member-x"}
	prov := &mockHetznerProvider{burstID: "ddeeff", hostname: "horizon-burst-ddeeff"}
	kc := fake.NewSimpleClientset()
	vc := &mockVelero{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cli.RunBurstForTest(ctx, newTestApp(), zt, prov, kc, vc, "sentio-systems")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	phase := k8s.ReadBurstPhase(context.Background(), kc)
	if phase != k8s.BurstPhaseIdle {
		t.Errorf("BurstPhase after signal rollback = %q, want Idle", phase)
	}
}

func TestBurstWritesPhase(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-eeff5566"
	zt := &mockZeroTier{waitID: "member-1"}
	prov := &mockHetznerProvider{burstID: "eeff5566", hostname: hostname, serverID: "1"}
	kc := fake.NewSimpleClientset(
		readyNode(hostname),
		workloadPod("p", "sentio-systems", hostname),
	)
	vc := &mockVelero{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	if err := cli.RunBurstForTest(context.Background(), newTestApp(), zt, prov, kc, vc, "sentio-systems"); err != nil {
		t.Fatalf("RunBurstForTest: %v", err)
	}

	var cmActions int
	for _, a := range kc.Actions() {
		if a.GetResource().Resource == "configmaps" {
			cmActions++
		}
	}
	if cmActions < 5 {
		t.Errorf("ConfigMap actions = %d, want >= 5 (BackingUp, Provisioning, Joining, Migrating, Running)", cmActions)
	}
	if got := k8s.ReadBurstPhase(context.Background(), kc); got != k8s.BurstPhaseRunning {
		t.Errorf("final phase = %q, want Running", got)
	}
}
