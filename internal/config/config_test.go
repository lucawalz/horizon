package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
kubeconfig: ~/.kube/config
thresholds:
  burst: 0.80
  scale_down: 0.40
  window: 5
  cooldown_minutes: 10
hetzner:
  api_token_env: HCLOUD_TOKEN
  server_type: cpx21
  location: nbg1
aws:
  region: eu-central-1
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Provider != "hetzner" {
		t.Errorf("Provider: got %q, want %q", cfg.Provider, "hetzner")
	}
	if cfg.Thresholds.Burst != 0.80 {
		t.Errorf("Thresholds.Burst: got %v, want 0.80", cfg.Thresholds.Burst)
	}
	if cfg.Thresholds.ScaleDown != 0.40 {
		t.Errorf("Thresholds.ScaleDown: got %v, want 0.40", cfg.Thresholds.ScaleDown)
	}
	if cfg.Thresholds.Window != 5 {
		t.Errorf("Thresholds.Window: got %v, want 5", cfg.Thresholds.Window)
	}
	if cfg.Thresholds.CooldownMinutes != 10 {
		t.Errorf("Thresholds.CooldownMinutes: got %v, want 10", cfg.Thresholds.CooldownMinutes)
	}
	if cfg.Hetzner.ServerType != "cpx21" {
		t.Errorf("Hetzner.ServerType: got %q, want %q", cfg.Hetzner.ServerType, "cpx21")
	}
	if cfg.AWS.Region != "eu-central-1" {
		t.Errorf("AWS.Region: got %q, want %q", cfg.AWS.Region, "eu-central-1")
	}
}

func TestInfraPath(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !filepath.IsAbs(cfg.InfraPath) {
		t.Errorf("InfraPath not absolute: %q", cfg.InfraPath)
	}
}

func TestInfraPathNonExistent(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: /nonexistent/path/that/does/not/exist
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for non-existent infra_path, got nil")
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when config file is missing, got nil")
	}
}

func TestWireGuardConfig(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
wireguard:
  hub_host: 192.168.20.1
  hub_user: root
  hub_public_key: DPHflo9uj/HXikf/3LXERxRe/t7KOueakDX5dMAdm3Y=
  interface: wg0
  listen_port: 51820
  subnet: 10.100.0.0/24
  master_ip: 192.168.20.10
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.WireGuard.HubHost != "192.168.20.1" {
		t.Errorf("HubHost: got %q, want %q", cfg.WireGuard.HubHost, "192.168.20.1")
	}
	if cfg.WireGuard.HubPublicKey != "DPHflo9uj/HXikf/3LXERxRe/t7KOueakDX5dMAdm3Y=" {
		t.Errorf("HubPublicKey: got %q", cfg.WireGuard.HubPublicKey)
	}
	if cfg.WireGuard.ListenPort != 51820 {
		t.Errorf("ListenPort: got %d, want 51820", cfg.WireGuard.ListenPort)
	}
	if cfg.WireGuard.Subnet != "10.100.0.0/24" {
		t.Errorf("Subnet: got %q, want %q", cfg.WireGuard.Subnet, "10.100.0.0/24")
	}
	if cfg.WireGuard.MasterIP != "192.168.20.10" {
		t.Errorf("MasterIP: got %q, want %q", cfg.WireGuard.MasterIP, "192.168.20.10")
	}
}

func TestWireGuardConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.WireGuard.HubHost != "" {
		t.Errorf("HubHost: got %q, want empty", cfg.WireGuard.HubHost)
	}
	if cfg.WireGuard.HubPublicKey != "" {
		t.Errorf("HubPublicKey: got %q, want empty", cfg.WireGuard.HubPublicKey)
	}
	if cfg.WireGuard.ListenPort != 0 {
		t.Errorf("ListenPort: got %d, want 0", cfg.WireGuard.ListenPort)
	}
}

func TestThresholdsMaxBurstNodes(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
thresholds:
  burst: 0.80
  scale_down: 0.40
  window: 5
  cooldown_minutes: 10
  max_burst_nodes: 3
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Thresholds.MaxBurstNodes != 3 {
		t.Errorf("MaxBurstNodes: got %d, want 3", cfg.Thresholds.MaxBurstNodes)
	}
}

func TestPushgatewayURL(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
pushgateway_url: http://kube-prometheus-stack-pushgateway.monitoring.svc:9091
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.PushgatewayURL != "http://kube-prometheus-stack-pushgateway.monitoring.svc:9091" {
		t.Errorf("PushgatewayURL: got %q, want %q", cfg.PushgatewayURL, "http://kube-prometheus-stack-pushgateway.monitoring.svc:9091")
	}
}

func TestK3sConfig(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
k3s:
  url: "https://10.147.20.1:6443"
  token: tok
  url_env: HORIZON_K3S_URL
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.K3s.URL != "https://10.147.20.1:6443" {
		t.Errorf("K3s.URL: got %q, want %q", cfg.K3s.URL, "https://10.147.20.1:6443")
	}
	if cfg.K3s.Token != "tok" {
		t.Errorf("K3s.Token: got %q, want %q", cfg.K3s.Token, "tok")
	}
	if cfg.K3s.URLEnv != "HORIZON_K3S_URL" {
		t.Errorf("K3s.URLEnv: got %q, want %q", cfg.K3s.URLEnv, "HORIZON_K3S_URL")
	}
}
