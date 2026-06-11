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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type mockZeroTier struct {
	authorizeErr   error
	deauthorizeErr error
	deleteErr      error
	authorizeCalls []string
	authorizeNames []string
	deauthCalls    []string
	deleteCalls    []string
}

func (m *mockZeroTier) Authorize(_ context.Context, _, memberID, name string) error {
	m.authorizeCalls = append(m.authorizeCalls, memberID)
	m.authorizeNames = append(m.authorizeNames, name)
	return m.authorizeErr
}

func (m *mockZeroTier) Deauthorize(_ context.Context, _, memberID string) error {
	m.deauthCalls = append(m.deauthCalls, memberID)
	return m.deauthorizeErr
}

func (m *mockZeroTier) DeleteMember(_ context.Context, _, memberID string) error {
	m.deleteCalls = append(m.deleteCalls, memberID)
	return m.deleteErr
}

type mockHetznerProvider struct {
	burstID      string
	hostname     string
	serverID     string
	serverIP     string
	memberID     string
	applyErr     error
	destroyCalls int
	destroyErr   error
	generateErr  error
}

func (m *mockHetznerProvider) SetRuntimeSecrets(zerotierNetworkID, sshPublicKey, k3sURL, k3sToken string) {
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

func (m *mockHetznerProvider) Hostname() string { return m.hostname }
func (m *mockHetznerProvider) BurstID() string  { return m.burstID }
func (m *mockHetznerProvider) ServerID() string         { return m.serverID }
func (m *mockHetznerProvider) ServerIP() string         { return m.serverIP }
func (m *mockHetznerProvider) ZeroTierMemberID() string { return m.memberID }

func newTestApp() *cli.App {
	return &cli.App{
		Config: &config.Config{
			ZeroTier: config.ZeroTierConfig{
				NetworkID:   "nw-test",
				APITokenEnv: "ZEROTIER_API_TOKEN",
				MasterIP:    "10.147.20.1",
			},
			K3s: config.K3sConfig{
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
	if !strings.Contains(out, "Authorize burst node in ZeroTier network") {
		t.Errorf("dry-run output missing zerotier-auth label:\n%s", out)
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
	zt := &mockZeroTier{}
	prov := &mockHetznerProvider{
		burstID:  "aabb1122",
		hostname: hostname,
		serverID: "99",
		memberID: "member-99",
	}
	kc := fake.NewSimpleClientset(readyNode(hostname))

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	if err := cli.RunUpForTest(context.Background(), newTestApp(), zt, prov, kc); err != nil {
		t.Fatalf("RunUpForTest: %v", err)
	}
	if len(zt.authorizeCalls) != 1 || zt.authorizeCalls[0] != "member-99" {
		t.Errorf("authorize calls = %v, want [member-99]", zt.authorizeCalls)
	}
	if len(zt.authorizeNames) != 1 || zt.authorizeNames[0] != hostname {
		t.Errorf("authorize names = %v, want [%s]", zt.authorizeNames, hostname)
	}
}

func TestUpRollbackOnTerraformFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	tfErr := errors.New("terraform apply failed")
	zt := &mockZeroTier{}
	prov := &mockHetznerProvider{
		burstID:  "ccdd3344",
		hostname: "horizon-burst-ccdd3344",
		memberID: "should-not-be-used",
		applyErr: tfErr,
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), zt, prov, fake.NewSimpleClientset())
	if err == nil {
		t.Fatal("expected error from terraform failure")
	}
	if len(zt.authorizeCalls) != 0 {
		t.Errorf("authorize must not run when terraform fails: %v", zt.authorizeCalls)
	}
	if len(zt.deauthCalls) != 0 {
		t.Errorf("deauthorize must not run when zerotier-auth never started: %v", zt.deauthCalls)
	}
	if prov.destroyCalls != 0 {
		t.Errorf("destroy must not run when terraform-apply itself failed: %v", prov.destroyCalls)
	}
	ids, _ := cli.ListStates(stateDir)
	if len(ids) != 0 {
		t.Errorf("state file written on failure: %v", ids)
	}
}

func TestUpRollbackOnZeroTierAuthFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	zt := &mockZeroTier{authorizeErr: errors.New("zt 401")}
	prov := &mockHetznerProvider{
		burstID:  "ddee5566",
		hostname: "horizon-burst-ddee5566",
		memberID: "member-77",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), zt, prov, fake.NewSimpleClientset())
	if err == nil {
		t.Fatal("expected error from zerotier-auth failure")
	}
	if len(zt.deauthCalls) != 0 {
		t.Errorf("deauth must not run when authorize itself failed: %v", zt.deauthCalls)
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
	zt := &mockZeroTier{}
	prov := &mockHetznerProvider{
		burstID:  "eeff5566",
		hostname: hostname,
		memberID: "member-late",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cli.RunUpForTest(ctx, newTestApp(), zt, prov, fake.NewSimpleClientset())
	if err == nil {
		t.Fatal("expected error from wait-node-ready with cancelled context")
	}
	if len(zt.deauthCalls) != 1 || zt.deauthCalls[0] != "member-late" {
		t.Errorf("deauth calls = %v, want [member-late]", zt.deauthCalls)
	}
	if len(zt.deleteCalls) != 1 || zt.deleteCalls[0] != "member-late" {
		t.Errorf("delete calls = %v, want [member-late]", zt.deleteCalls)
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
	zt := &mockZeroTier{}
	prov := &mockHetznerProvider{
		burstID:  "aabb1234",
		hostname: hostname,
		serverID: "99",
		memberID: "member-ok",
	}
	kc := fake.NewSimpleClientset(readyNode(hostname))

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://10.147.20.1:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	out := captureStdout(func() {
		if err := cli.RunUpForTest(context.Background(), newTestApp(), zt, prov, kc); err != nil {
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
	if st.ZeroTierMemberID != "member-ok" {
		t.Errorf("state.ZeroTierMemberID = %q, want member-ok", st.ZeroTierMemberID)
	}
	if st.HetznerServerID != "99" {
		t.Errorf("state.HetznerServerID = %q, want 99", st.HetznerServerID)
	}
}
