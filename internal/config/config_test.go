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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for non-existent infra_path, got nil")
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when config file is missing, got nil")
	}
}

func TestZeroTierConfig(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
zerotier:
  network_id: abc123
  api_token_env: ZEROTIER_API_TOKEN
  master_ip: 10.147.20.1
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ZeroTier.NetworkID != "abc123" {
		t.Errorf("NetworkID: got %q, want %q", cfg.ZeroTier.NetworkID, "abc123")
	}
	if cfg.ZeroTier.APITokenEnv != "ZEROTIER_API_TOKEN" {
		t.Errorf("APITokenEnv: got %q, want %q", cfg.ZeroTier.APITokenEnv, "ZEROTIER_API_TOKEN")
	}
	if cfg.ZeroTier.MasterIP != "10.147.20.1" {
		t.Errorf("MasterIP: got %q, want %q", cfg.ZeroTier.MasterIP, "10.147.20.1")
	}
}

func TestZeroTierConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `
provider: hetzner
infra_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ZeroTier.NetworkID != "" {
		t.Errorf("NetworkID: got %q, want empty", cfg.ZeroTier.NetworkID)
	}
	if cfg.ZeroTier.APITokenEnv != "" {
		t.Errorf("APITokenEnv: got %q, want empty", cfg.ZeroTier.APITokenEnv)
	}
	if cfg.ZeroTier.MasterIP != "" {
		t.Errorf("MasterIP: got %q, want empty", cfg.ZeroTier.MasterIP)
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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

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
