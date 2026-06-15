package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/wireguard"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type mockPeerManager struct {
	addErr      error
	removeErr   error
	addCalls    []string
	removeCalls []string
}

func (m *mockPeerManager) AddPeer(_ context.Context, peer wireguard.Peer) error {
	m.addCalls = append(m.addCalls, peer.PublicKey)
	return m.addErr
}

func (m *mockPeerManager) RemovePeer(_ context.Context, publicKey string) error {
	m.removeCalls = append(m.removeCalls, publicKey)
	return m.removeErr
}

type mockHetznerProvider struct {
	burstID      string
	hostname     string
	serverID     string
	serverIP     string
	pubKey       string
	wgIP         string
	applyErr     error
	destroyCalls int
	destroyErr   error
	generateErr  error
}

func (m *mockHetznerProvider) SetRuntimeSecrets(hubPublicKey, wgIP, sshPublicKey, k3sURL, k3sToken string) {
}

func (m *mockHetznerProvider) GenerateTFVars() (map[string]string, error) {
	if m.generateErr != nil {
		return nil, m.generateErr
	}
	return map[string]string{"burst_id": m.burstID}, nil
}

func (m *mockHetznerProvider) Apply(_ context.Context, _ map[string]string) error {
	return m.applyErr
}

func (m *mockHetznerProvider) Destroy(_ context.Context) error {
	m.destroyCalls++
	return m.destroyErr
}

func (m *mockHetznerProvider) Hostname() string           { return m.hostname }
func (m *mockHetznerProvider) BurstID() string            { return m.burstID }
func (m *mockHetznerProvider) ServerID() string           { return m.serverID }
func (m *mockHetznerProvider) ServerIP() string           { return m.serverIP }
func (m *mockHetznerProvider) WireGuardPublicKey() string { return m.pubKey }
func (m *mockHetznerProvider) WireGuardIP() string        { return m.wgIP }

func newTestApp() *cli.App {
	return &cli.App{
		Config: &config.Config{
			Pools: config.PoolDefaults{Cluster: "burst"},
			WireGuard: config.WireGuardConfig{
				HubHost:      "10.20.0.1",
				HubUser:      "root",
				HubPublicKey: "DPHflo9uj/HXikf/3LXERxRe/t7KOueakDX5dMAdm3Y=",
				Interface:    "wg0",
				ListenPort:   51820,
				Subnet:       "10.100.0.0/24",
				MasterIP:     "10.20.0.10",
			},
			K3s: config.K3sConfig{
				URL:       "https://10.20.0.10:6443",
				URLEnv:    "HORIZON_K3S_URL",
				TokenEnv:  "HORIZON_K3S_TOKEN",
				SSHKeyEnv: "HORIZON_SSH_PUBLIC_KEY",
			},
		},
	}
}

func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func TestUpDryRun(t *testing.T) {
	app := newTestApp()
	out := captureStdout(func() {
		cli.RunUpDryRunForTest(app)
	})
	for i := 1; i <= 5; i++ {
		want := fmt.Sprintf("[dry-run] Step %d:", i)
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Register burst node as WireGuard peer on hub") {
		t.Errorf("dry-run output missing wg-peer-add label:\n%s", out)
	}
	if !strings.Contains(out, "[dry-run] No actions executed.") {
		t.Errorf("missing trailing line:\n%s", out)
	}
}

func TestUpStepOrder(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-aabb1122"
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{
		burstID:  "aabb1122",
		hostname: hostname,
		serverID: "99",
		pubKey:   "pub-99",
	}
	kc := fake.NewSimpleClientset(readyNode(hostname))

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.20.0.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	if err := cli.RunUpForTest(context.Background(), newTestApp(), pm, prov, kc); err != nil {
		t.Fatalf("RunUpForTest: %v", err)
	}
	if len(pm.addCalls) != 1 || pm.addCalls[0] != "pub-99" {
		t.Errorf("AddPeer calls = %v, want [pub-99]", pm.addCalls)
	}
}

func TestUpRollbackOnTerraformFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	tfErr := errors.New("terraform apply failed")
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{
		burstID:  "ccdd3344",
		hostname: "horizon-burst-ccdd3344",
		pubKey:   "should-not-be-used",
		applyErr: tfErr,
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.20.0.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), pm, prov, fake.NewSimpleClientset())
	if err == nil {
		t.Fatal("expected error from terraform failure")
	}
	if len(pm.addCalls) != 0 {
		t.Errorf("AddPeer must not run when terraform fails: %v", pm.addCalls)
	}
	if len(pm.removeCalls) != 0 {
		t.Errorf("RemovePeer must not run when wg-peer-add never started: %v", pm.removeCalls)
	}
	if prov.destroyCalls != 0 {
		t.Errorf("destroy must not run when terraform-apply itself failed: %v", prov.destroyCalls)
	}
	ids, _ := cli.ListStates(stateDir)
	if len(ids) != 0 {
		t.Errorf("state file written on failure: %v", ids)
	}
}

func TestUpRollbackOnWGPeerAddFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	pm := &mockPeerManager{addErr: errors.New("ssh failed")}
	prov := &mockHetznerProvider{
		burstID:  "ddee5566",
		hostname: "horizon-burst-ddee5566",
		pubKey:   "pub-77",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.20.0.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), pm, prov, fake.NewSimpleClientset())
	if err == nil {
		t.Fatal("expected error from wg-peer-add failure")
	}
	if len(pm.removeCalls) != 0 {
		t.Errorf("RemovePeer must not run when AddPeer itself failed: %v", pm.removeCalls)
	}
	if prov.destroyCalls != 1 {
		t.Errorf("destroy calls = %d, want 1 (terraform-apply rollback)", prov.destroyCalls)
	}
}

func TestUpRollbackOnWaitNodeReadyTimeout(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-eeff5566"
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{
		burstID:  "eeff5566",
		hostname: hostname,
		pubKey:   "pub-late",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.20.0.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cli.RunUpForTest(ctx, newTestApp(), pm, prov, fake.NewSimpleClientset())
	if err == nil {
		t.Fatal("expected error from wait-node-ready with cancelled context")
	}
	if len(pm.removeCalls) != 1 || pm.removeCalls[0] != "pub-late" {
		t.Errorf("RemovePeer calls = %v, want [pub-late]", pm.removeCalls)
	}
	if prov.destroyCalls != 1 {
		t.Errorf("destroy calls = %d, want 1", prov.destroyCalls)
	}
	ids, _ := cli.ListStates(stateDir)
	if len(ids) != 0 {
		t.Errorf("state file written on failure: %v", ids)
	}
}

func TestUpWritesStateOnSuccess(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-aabb1234"
	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{
		burstID:  "aabb1234",
		hostname: hostname,
		serverID: "99",
		pubKey:   "pub-ok",
	}
	kc := fake.NewSimpleClientset(readyNode(hostname))

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.20.0.10:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	out := captureStdout(func() {
		if err := cli.RunUpForTest(context.Background(), newTestApp(), pm, prov, kc); err != nil {
			t.Errorf("RunUpForTest: %v", err)
		}
	})

	if !strings.Contains(out, "burst_id: aabb1234") {
		t.Errorf("stdout missing burst_id line:\n%s", out)
	}

	st, err := cli.ReadState(stateDir, "aabb1234")
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.BurstID != "aabb1234" {
		t.Errorf("state.BurstID = %q", st.BurstID)
	}
	if st.WireGuardPubKey != "pub-ok" {
		t.Errorf("state.WireGuardPubKey = %q, want pub-ok", st.WireGuardPubKey)
	}
	if st.WireGuardIP == "" {
		t.Error("state.WireGuardIP must be populated")
	}
	if st.HetznerServerID != "99" {
		t.Errorf("state.HetznerServerID = %q, want 99", st.HetznerServerID)
	}
}
