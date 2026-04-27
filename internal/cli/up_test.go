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
	"github.com/lucawalz/horizon/internal/headscale"
	"k8s.io/client-go/kubernetes"
)

type mockHeadscaler struct {
	createKey    headscale.PreAuthKey
	createErr    error
	revokeErr    error
	findNodeID   string
	findErr      error
	deleteErr    error
	revokeCalls  []string
	deleteCalls  []string
}

func (m *mockHeadscaler) CreatePreAuthKey(_ context.Context, user string) (headscale.PreAuthKey, error) {
	return m.createKey, m.createErr
}

func (m *mockHeadscaler) RevokePreAuthKey(_ context.Context, user, key string) error {
	m.revokeCalls = append(m.revokeCalls, key)
	return m.revokeErr
}

func (m *mockHeadscaler) FindNodeByHostname(_ context.Context, hostname string) (string, error) {
	return m.findNodeID, m.findErr
}

func (m *mockHeadscaler) DeleteNode(_ context.Context, nodeID string) error {
	m.deleteCalls = append(m.deleteCalls, nodeID)
	return m.deleteErr
}

type mockHetznerProvider struct {
	burstID   string
	hostname  string
	serverID  string
	applyErr  error
	destroyCalls int
	destroyErr   error
	generateErr  error
	secretsSet   bool
}

func (m *mockHetznerProvider) SetRuntimeSecrets(preAuthKey, sshPublicKey, k3sURL, k3sToken string) {
	m.secretsSet = true
}

func (m *mockHetznerProvider) GenerateTFVars() (map[string]string, error) {
	if m.generateErr != nil {
		return nil, m.generateErr
	}
	return map[string]string{"burst_id": m.burstID}, nil
}

func (m *mockHetznerProvider) Apply(_ map[string]string) error {
	return m.applyErr
}

func (m *mockHetznerProvider) Destroy() error {
	m.destroyCalls++
	return m.destroyErr
}

func (m *mockHetznerProvider) Hostname() string { return m.hostname }
func (m *mockHetznerProvider) BurstID() string  { return m.burstID }
func (m *mockHetznerProvider) ServerID() string { return m.serverID }

func newTestApp() *cli.App {
	return &cli.App{
		Config: &config.Config{
			Headscale: config.HeadscaleConfig{
				APIURL:    "http://headscale.test",
				APIKeyEnv: "TEST_HEADSCALE_KEY",
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
	if !strings.Contains(out, "[dry-run] No actions executed.") {
		t.Errorf("missing trailing line in output:\n%s", out)
	}
}

func TestUpStepOrder(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hs := &mockHeadscaler{
		createKey: headscale.PreAuthKey{ID: "1", Key: "key-abc", User: "burst-nodes"},
	}
	prov := &mockHetznerProvider{
		burstID:  "aabb1122",
		hostname: "horizon-burst-aabb1122",
		serverID: "99",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://master:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), hs, prov, nil)
	if err != nil {
		t.Fatalf("RunUpForTest: %v", err)
	}
}

func TestUpRollbackOnTerraformFailure(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	tfErr := errors.New("terraform apply failed")
	hs := &mockHeadscaler{
		createKey: headscale.PreAuthKey{ID: "2", Key: "key-xyz", User: "burst-nodes"},
	}
	prov := &mockHetznerProvider{
		burstID:  "ccdd3344",
		hostname: "horizon-burst-ccdd3344",
		applyErr: tfErr,
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://master:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), hs, prov, nil)
	if err == nil {
		t.Fatal("expected error from terraform failure")
	}
	if len(hs.revokeCalls) != 1 || hs.revokeCalls[0] != "key-xyz" {
		t.Errorf("revoke calls = %v, want [key-xyz]", hs.revokeCalls)
	}
	ids, _ := cli.ListStates(stateDir)
	if len(ids) != 0 {
		t.Errorf("state file written on failure: %v", ids)
	}
}

func TestUpRollbackOnWaitNodeReadyTimeout(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hs := &mockHeadscaler{
		createKey: headscale.PreAuthKey{ID: "3", Key: "key-wait", User: "burst-nodes"},
	}
	prov := &mockHetznerProvider{
		burstID:  "eeff5566",
		hostname: "horizon-burst-eeff5566",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://master:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	err := cli.RunUpForTest(context.Background(), newTestApp(), hs, prov, nil)
	if err == nil {
		t.Fatal("expected error from wait-node-ready timeout (nil kc)")
	}
	if prov.destroyCalls != 1 {
		t.Errorf("destroy calls = %d, want 1", prov.destroyCalls)
	}
	if len(hs.revokeCalls) != 1 || hs.revokeCalls[0] != "key-wait" {
		t.Errorf("revoke calls = %v, want [key-wait]", hs.revokeCalls)
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

	hs := &mockHeadscaler{
		createKey:  headscale.PreAuthKey{ID: "4", Key: "key-ok", User: "burst-nodes"},
		findNodeID: "42",
	}
	prov := &mockHetznerProvider{
		burstID:  "aabb1234",
		hostname: "horizon-burst-aabb1234",
		serverID: "99",
	}

	t.Setenv("HORIZON_SSH_PUBLIC_KEY", "ssh-ed25519 AAAA")
	t.Setenv("HORIZON_K3S_URL", "https://master:6443")
	t.Setenv("HORIZON_K3S_TOKEN", "tok")

	var kc kubernetes.Interface
	_ = kc

	out := captureStdout(func() {
		if err := cli.RunUpForTest(context.Background(), newTestApp(), hs, prov, nil); err != nil {
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
		t.Errorf("state.BurstID = %q, want aabb1234", st.BurstID)
	}
	if st.HeadscalePreAuthKey != "key-ok" {
		t.Errorf("state.HeadscalePreAuthKey = %q, want key-ok", st.HeadscalePreAuthKey)
	}
	if st.HetznerServerID != "99" {
		t.Errorf("state.HetznerServerID = %q, want 99", st.HetznerServerID)
	}
}
