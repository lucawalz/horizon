package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	content := `
repo_path: ` + dir + `
kubeconfig: ~/.kube/config
cluster: prod
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cluster != "prod" {
		t.Errorf("Cluster: got %q, want prod", cfg.Cluster)
	}
}

func TestRepoPath(t *testing.T) {
	dir := t.TempDir()
	content := `
repo_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !filepath.IsAbs(cfg.RepoPath) {
		t.Errorf("RepoPath not absolute: %q", cfg.RepoPath)
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

func TestPoolDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `
repo_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
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
repo_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
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
repo_path: ` + dir + `
cluster: prod
pools:
  namespace: capi-system
  cluster: edge
  default_type: elastic
  types:
    elastic: edge-elastic
    reserved: edge-reserved
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
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

func TestThemeDefaultsToAuto(t *testing.T) {
	dir := t.TempDir()
	content := "repo_path: " + dir + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Theme != config.ThemeAuto {
		t.Errorf("Theme: got %q, want %q", cfg.Theme, config.ThemeAuto)
	}
}

func TestThemeInvalidRejected(t *testing.T) {
	dir := t.TempDir()
	content := "repo_path: " + dir + "\ntheme: neon\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for invalid theme, got nil")
	}
}

func TestSaveRoundTripsTheme(t *testing.T) {
	dir := t.TempDir()
	content := "repo_path: " + dir + "\ncluster: prod\ntheme: dark\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if err := cfg.SetTheme(config.ThemeLight); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if reloaded.Theme != config.ThemeLight {
		t.Errorf("Theme after reload: got %q, want %q", reloaded.Theme, config.ThemeLight)
	}
	if reloaded.Cluster != "prod" {
		t.Errorf("Cluster after reload: got %q, want prod", reloaded.Cluster)
	}
}

func TestSetThemeRejectsInvalid(t *testing.T) {
	cfg := &config.Config{}
	if err := cfg.SetTheme("neon"); err == nil {
		t.Fatal("expected error for invalid theme")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Run("HORIZON_CONFIG_DIR wins", func(t *testing.T) {
		t.Setenv("HORIZON_CONFIG_DIR", "/tmp/horizon-cfg")
		t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
		if got := config.DefaultConfigPath(); got != "/tmp/horizon-cfg/config.yaml" {
			t.Errorf("got %q, want /tmp/horizon-cfg/config.yaml", got)
		}
	})
	t.Run("XDG_CONFIG_HOME second", func(t *testing.T) {
		t.Setenv("HORIZON_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
		if got := config.DefaultConfigPath(); got != "/tmp/xdg/horizon/config.yaml" {
			t.Errorf("got %q, want /tmp/xdg/horizon/config.yaml", got)
		}
	})
	t.Run("home fallback", func(t *testing.T) {
		t.Setenv("HORIZON_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/tmp/home")
		if got := config.DefaultConfigPath(); got != "/tmp/home/.config/horizon/config.yaml" {
			t.Errorf("got %q, want /tmp/home/.config/horizon/config.yaml", got)
		}
	})
}

func TestLoadNotConfigured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if !errors.Is(err, config.ErrNotConfigured) {
		t.Fatalf("Load() error = %v, want ErrNotConfigured", err)
	}
}

func TestLoadParseErrorIsNotNotConfigured(t *testing.T) {
	dir := t.TempDir()
	content := "repo_path: [unterminated\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for malformed yaml, got nil")
	}
	if errors.Is(err, config.ErrNotConfigured) {
		t.Errorf("parse error must not be ErrNotConfigured: %v", err)
	}
}

func TestDefaultSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "nested", "horizon")
	path := filepath.Join(cfgDir, "config.yaml")

	cfg := config.Default(path)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(cfgDir); err != nil {
		t.Fatalf("config dir not created: %v", err)
	}

	t.Setenv("HORIZON_CONFIG_DIR", cfgDir)
	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Pools.Namespace != "caph-system" {
		t.Errorf("Pools.Namespace: got %q, want caph-system", reloaded.Pools.Namespace)
	}
	if reloaded.Pools.DefaultType != "reserved" {
		t.Errorf("Pools.DefaultType: got %q, want reserved", reloaded.Pools.DefaultType)
	}
	if reloaded.Cluster != "burst" {
		t.Errorf("Cluster: got %q, want burst", reloaded.Cluster)
	}
	if reloaded.Theme != config.ThemeAuto {
		t.Errorf("Theme: got %q, want %q", reloaded.Theme, config.ThemeAuto)
	}
}

func TestLegacyInfraPathWithoutRepoPathFailsFast(t *testing.T) {
	dir := t.TempDir()
	content := `
infra_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when infra_path is set without repo_path, got nil")
	}
	if !strings.Contains(err.Error(), "repo_path") {
		t.Errorf("error %q must mention repo_path", err.Error())
	}
}

func TestLegacyBedrockPathWithoutRepoPathFailsFast(t *testing.T) {
	dir := t.TempDir()
	content := `
bedrock_path: ` + dir + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when bedrock_path is set without repo_path, got nil")
	}
	if !strings.Contains(err.Error(), "repo_path") {
		t.Errorf("error %q must mention repo_path", err.Error())
	}
}

func TestEmptyLegacyBedrockPathLoadsCleanly(t *testing.T) {
	dir := t.TempDir()
	content := `
repo_path: ` + dir + `
bedrock_path: ""
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !filepath.IsAbs(cfg.RepoPath) {
		t.Errorf("RepoPath not absolute: %q", cfg.RepoPath)
	}
}
