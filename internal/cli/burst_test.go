package cli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

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
	if !strings.Contains(out, "WireGuard") {
		t.Errorf("dry-run output missing WireGuard step:\n%s", out)
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

func TestBurstProvider_UsesSuppliedBurstID(t *testing.T) {
	id := "5d855091"
	got, err := cli.NewBurstProviderBurstIDForTest(newTestApp(), id)
	if err != nil {
		t.Fatalf("NewBurstProviderBurstIDForTest: %v", err)
	}
	if got != id {
		t.Fatalf("provider burst_id = %q, want %q (node name/workspace/state must derive from it)", got, id)
	}
}

func TestBurstProvider_AutoGeneratesWhenNoID(t *testing.T) {
	got, err := cli.NewBurstProviderBurstIDForTest(newTestApp(), "")
	if err != nil {
		t.Fatalf("NewBurstProviderBurstIDForTest: %v", err)
	}
	if got == "" {
		t.Fatal("provider must auto-generate a burst_id when none supplied")
	}
}

func TestBurstProvider_RejectsInvalidID(t *testing.T) {
	if _, err := cli.NewBurstProviderBurstIDForTest(newTestApp(), "NOT-HEX!!"); err == nil {
		t.Fatal("expected error for invalid burst_id")
	}
}

func TestBurstCmd_InvalidBurstIDRejected(t *testing.T) {
	app := &cli.App{Config: &config.Config{Provider: "hetzner"}}
	cmd := cli.NewBurstCmdForTest(app)
	cmd.SetArgs([]string{"--workload", "sentio-systems", "--burst-id", "NOT-HEX!!"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --burst-id fails the burst id pattern")
	}
	if !strings.Contains(err.Error(), "burst_id") {
		t.Errorf("error %q does not mention burst_id", err.Error())
	}
}

func TestBurstStepOrder(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-aabb1234"
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "aabb1234", hostname: hostname, serverID: "99", pubKey: "pub-99"}
	kc := fake.NewSimpleClientset(
		readyNode(hostname),
		workloadPod("app1", "sentio-systems", hostname),
	)
	vc := &fakeVeleroClient{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://192.168.20.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	if err := cli.RunBurstForTest(context.Background(), newTestApp(), pm, prov, kc, vc, "sentio-systems"); err != nil {
		t.Fatalf("RunBurstForTest: %v", err)
	}
	if len(pm.addCalls) != 1 || pm.addCalls[0] != "pub-99" {
		t.Errorf("AddPeer calls = %v, want [pub-99]", pm.addCalls)
	}
	if !vc.waited {
		t.Error("burst must trigger a velero backup")
	}
	spec := vc.triggeredBackupSpec
	if len(spec.IncludedNamespaces) != 1 || spec.IncludedNamespaces[0] != "sentio-systems" {
		t.Errorf("backup IncludedNamespaces = %v, want [sentio-systems]", spec.IncludedNamespaces)
	}
	if spec.StorageLocation != "default" {
		t.Errorf("backup StorageLocation = %q, want default", spec.StorageLocation)
	}
	phases, _ := k8s.ReadBurstPhases(context.Background(), kc)
	if phases["aabb1234"] != k8s.BurstPhaseRunning {
		t.Errorf("final phase = %q, want Running", phases["aabb1234"])
	}
}

func TestBurstRollback_OnTerraformFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	tfErr := errors.New("terraform apply failed")
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "ccdd3344", hostname: "horizon-burst-ccdd3344", pubKey: "should-not-be-used", applyErr: tfErr}
	kc := fake.NewSimpleClientset()
	vc := &fakeVeleroClient{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://192.168.20.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunBurstForTest(context.Background(), newTestApp(), pm, prov, kc, vc, "sentio-systems")
	if err == nil {
		t.Fatal("expected error from terraform failure")
	}
	if len(pm.addCalls) != 0 {
		t.Errorf("AddPeer must not run when terraform fails: %v", pm.addCalls)
	}
	if prov.destroyCalls != 0 {
		t.Errorf("destroy must not run when terraform-apply itself failed: %v", prov.destroyCalls)
	}
	phases, _ := k8s.ReadBurstPhases(context.Background(), kc)
	if _, ok := phases["ccdd3344"]; ok {
		t.Errorf("burst phase after rollback should be pruned, got %v", phases)
	}
}

func TestBurstSignalRollback(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "ddeeff", hostname: "horizon-burst-ddeeff", pubKey: "pub-x"}
	kc := fake.NewSimpleClientset()
	vc := &fakeVeleroClient{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://192.168.20.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cli.RunBurstForTest(ctx, newTestApp(), pm, prov, kc, vc, "sentio-systems")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	phases, _ := k8s.ReadBurstPhases(context.Background(), kc)
	if _, ok := phases["ddeeff"]; ok {
		t.Errorf("burst phase after signal rollback should be pruned, got %v", phases)
	}
}

func TestBurstWritesPhase(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-eeff5566"
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "eeff5566", hostname: hostname, serverID: "1", pubKey: "pub-1"}
	kc := fake.NewSimpleClientset(
		readyNode(hostname),
		workloadPod("p", "sentio-systems", hostname),
	)
	vc := &fakeVeleroClient{}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://192.168.20.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	if err := cli.RunBurstForTest(context.Background(), newTestApp(), pm, prov, kc, vc, "sentio-systems"); err != nil {
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
	phases, _ := k8s.ReadBurstPhases(context.Background(), kc)
	if phases["eeff5566"] != k8s.BurstPhaseRunning {
		t.Errorf("final phase = %q, want Running", phases["eeff5566"])
	}
}
