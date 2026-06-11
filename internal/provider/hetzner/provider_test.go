package hetzner_test

import (
	"testing"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

var _ provider.Provider = (*hetzner.Provider)(nil)

func TestProviderImplementsInterface(t *testing.T) {
	_ = (*hetzner.Provider)(nil)
}

func newTestConfig() *config.Config {
	return &config.Config{
		Hetzner: config.HetznerConfig{
			APITokenEnv: "HCLOUD_TOKEN",
			ServerType:  "cx22",
			Location:    "fsn1",
		},
		ZeroTier: config.ZeroTierConfig{
			NetworkID:   "nw-abc",
			APITokenEnv: "ZEROTIER_API_TOKEN",
		},
	}
}

func TestGenerateTFVars(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "test-token")

	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())
	p.SetRuntimeSecrets("nw-abc", "ssh-rsa AAAA", "https://10.147.20.1:6443", "k3s-token-xyz")

	vars, err := p.GenerateTFVars()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vars) != 8 {
		t.Fatalf("expected 8 keys, got %d: %v", len(vars), vars)
	}
	required := map[string]string{
		"server_type":         "cx22",
		"location":            "fsn1",
		"flake_ref":           "main",
		"ssh_public_key":      "ssh-rsa AAAA",
		"zerotier_network_id": "nw-abc",
		"k3s_url":             "https://10.147.20.1:6443",
		"k3s_token":           "k3s-token-xyz",
	}
	for k, want := range required {
		if got := vars[k]; got != want {
			t.Errorf("key %q: want %q got %q", k, want, got)
		}
	}
	if vars["burst_id"] == "" {
		t.Error("burst_id must be non-empty")
	}
	for _, forbidden := range []string{"headscale_preauthkey", "headscale_server_url"} {
		if _, ok := vars[forbidden]; ok {
			t.Errorf("forbidden legacy key %q present in TFVars", forbidden)
		}
	}
}

func TestGenerateTFVarsMissingNetworkID(t *testing.T) {
	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())
	_, err := p.GenerateTFVars()
	if err == nil {
		t.Fatal("expected error when zerotier network_id missing")
	}
	if !containsAll(err.Error(), "zerotier", "network_id") {
		t.Errorf("error %q must contain 'zerotier' and 'network_id'", err.Error())
	}
}

func TestGenerateTFVarsBurstIDStable(t *testing.T) {
	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())
	p.SetRuntimeSecrets("nw-abc", "ssh-rsa AAAA", "https://10.147.20.1:6443", "tok")

	v1, err := p.GenerateTFVars()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	v2, err := p.GenerateTFVars()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if v1["burst_id"] != v2["burst_id"] {
		t.Errorf("burst_id changed between calls: %q vs %q", v1["burst_id"], v2["burst_id"])
	}
}

func TestProviderServerID(t *testing.T) {
	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())
	if p.ServerID() != "" {
		t.Fatalf("expected empty ServerID before Apply, got %q", p.ServerID())
	}
	p.SetServerIDForTest("42")
	if p.ServerID() != "42" {
		t.Fatalf("expected ServerID to be '42', got %q", p.ServerID())
	}
}

func TestConstructorsDoNotGenerateIdentity(t *testing.T) {
	cfg := newTestConfig()

	p := hetzner.New(cfg, t.TempDir())
	if p.ZeroTierMemberID() != "" {
		t.Errorf("New must not generate an identity, got member id %q", p.ZeroTierMemberID())
	}

	pb, err := hetzner.NewWithBurstID(cfg, t.TempDir(), "deadbeef")
	if err != nil {
		t.Fatalf("NewWithBurstID: %v", err)
	}
	if pb.ZeroTierMemberID() != "" {
		t.Errorf("NewWithBurstID must not generate an identity, got member id %q", pb.ZeroTierMemberID())
	}
}

func TestGenerateTFVarsWithoutIdentity(t *testing.T) {
	cfg := newTestConfig()
	p, err := hetzner.NewWithBurstID(cfg, t.TempDir(), "deadbeef")
	if err != nil {
		t.Fatalf("NewWithBurstID: %v", err)
	}
	p.SetRuntimeSecrets("nw-abc", "ssh-rsa AAAA", "https://10.147.20.1:6443", "tok")
	if _, err := p.GenerateTFVars(); err != nil {
		t.Fatalf("GenerateTFVars must not require an identity: %v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
