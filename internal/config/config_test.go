package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
kubeconfig: ~/.kube/config
thresholds:
  burst: 0.80
  scale_down: 0.40
  window: 5
  cooldown_minutes: 10
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
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
}

func TestBedrockPath(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !filepath.IsAbs(cfg.BedrockPath) {
		t.Errorf("BedrockPath not absolute: %q", cfg.BedrockPath)
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

func TestThresholdsMaxBurstNodes(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
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

func TestPoolDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Pools.Namespace != "caph-system" {
		t.Errorf("Pools.Namespace: got %q, want caph-system", cfg.Pools.Namespace)
	}
	if cfg.Pools.Cluster != "burst" {
		t.Errorf("Pools.Cluster: got %q, want burst", cfg.Pools.Cluster)
	}
	if cfg.Pools.DefaultType != "reserved" {
		t.Errorf("Pools.DefaultType: got %q, want reserved", cfg.Pools.DefaultType)
	}
	if got := cfg.Pools.Types["elastic"]; got != "elastic-workers" {
		t.Errorf("Pools.Types[elastic]: got %q, want elastic-workers", got)
	}
	if got := cfg.Pools.Types["reserved"]; got != "reserved-workers" {
		t.Errorf("Pools.Types[reserved]: got %q, want reserved-workers", got)
	}
	if cfg.Cluster != "burst" {
		t.Errorf("Cluster: got %q, want burst", cfg.Cluster)
	}
}

func TestPoolResolve(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if md, err := cfg.Pools.Resolve(""); err != nil || md != "reserved-workers" {
		t.Errorf("Resolve(\"\") = %q, %v; want reserved-workers, nil", md, err)
	}
	if md, err := cfg.Pools.Resolve("elastic"); err != nil || md != "elastic-workers" {
		t.Errorf("Resolve(elastic) = %q, %v; want elastic-workers, nil", md, err)
	}
	if _, err := cfg.Pools.Resolve("bogus"); err == nil {
		t.Fatal("expected error for unknown pool type")
	} else if !strings.Contains(err.Error(), "unknown pool type") {
		t.Errorf("error %q must mention unknown pool type", err.Error())
	}
}

func TestPoolOverrides(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
cluster: prod
pools:
  namespace: capi-system
  cluster: edge
  default_type: elastic
  types:
    elastic: edge-elastic
    reserved: edge-reserved
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Pools.Namespace != "capi-system" {
		t.Errorf("Pools.Namespace: got %q, want capi-system", cfg.Pools.Namespace)
	}
	if cfg.Pools.Cluster != "edge" {
		t.Errorf("Pools.Cluster: got %q, want edge", cfg.Pools.Cluster)
	}
	if cfg.Pools.DefaultType != "elastic" {
		t.Errorf("Pools.DefaultType: got %q, want elastic", cfg.Pools.DefaultType)
	}
	if got := cfg.Pools.Types["reserved"]; got != "edge-reserved" {
		t.Errorf("Pools.Types[reserved]: got %q, want edge-reserved", got)
	}
	if cfg.Cluster != "prod" {
		t.Errorf("Cluster: got %q, want prod", cfg.Cluster)
	}
}

func TestLegacyInfraPathWithoutBedrockFailsFast(t *testing.T) {
	dir := t.TempDir()
	content := `
infra_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when infra_path is set without bedrock_path, got nil")
	}
	if !strings.Contains(err.Error(), "bedrock_path") {
		t.Errorf("error %q must mention bedrock_path", err.Error())
	}
}
