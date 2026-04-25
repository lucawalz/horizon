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
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when config file is missing, got nil")
	}
}
