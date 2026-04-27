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
		Headscale: config.HeadscaleConfig{
			ServerURL: "https://headscale.example.com",
		},
	}
}

func TestGenerateTFVars(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "test-token")
	t.Setenv("HEADSCALE_PREAUTHKEY", "ts-key-abc")
	t.Setenv("K3S_URL", "https://master:6443")
	t.Setenv("K3S_TOKEN", "k3s-token-xyz")
	t.Setenv("SSH_PUBLIC_KEY", "ssh-rsa AAAA")

	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())
	p.SetRuntimeSecrets("ts-key-abc", "ssh-rsa AAAA", "https://master:6443", "k3s-token-xyz")

	vars, err := p.GenerateTFVars()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vars) != 9 {
		t.Fatalf("expected 9 keys, got %d: %v", len(vars), vars)
	}

	required := map[string]string{
		"server_type":          "cx22",
		"location":             "fsn1",
		"flake_ref":            "main",
		"ssh_public_key":       "ssh-rsa AAAA",
		"headscale_preauthkey": "ts-key-abc",
		"headscale_server_url": "https://headscale.example.com",
		"k3s_url":              "https://master:6443",
		"k3s_token":            "k3s-token-xyz",
	}
	for k, want := range required {
		if got := vars[k]; got != want {
			t.Errorf("key %q: want %q got %q", k, want, got)
		}
	}
	if vars["burst_id"] == "" {
		t.Error("burst_id must be non-empty")
	}
}

func TestGenerateTFVarsMissingPreAuthKey(t *testing.T) {
	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())

	_, err := p.GenerateTFVars()
	if err == nil {
		t.Fatal("expected error when preauth key is missing")
	}
	errStr := err.Error()
	if !containsAll(errStr, "headscale", "preauth") {
		t.Errorf("error %q must contain 'headscale' and 'preauth'", errStr)
	}
}

func TestGenerateTFVarsBurstIDStable(t *testing.T) {
	cfg := newTestConfig()
	p := hetzner.New(cfg, t.TempDir())
	p.SetRuntimeSecrets("key", "ssh-rsa AAAA", "https://master:6443", "token")

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
