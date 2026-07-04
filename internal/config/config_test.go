package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	content := `
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
	content := "kubeconfig: \"\"\n"
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
	if got := cfg.Pools.Types["reserved"]; got != "reserved-workers" {
		t.Errorf("Pools.Types[reserved]: got %q, want reserved-workers", got)
	}
	if _, ok := cfg.Pools.Types["elastic"]; ok {
		t.Errorf("Pools.Types should not default an elastic entry, got %v", cfg.Pools.Types)
	}
	if cfg.Cluster != "burst" {
		t.Errorf("Cluster: got %q, want burst", cfg.Cluster)
	}
}

func TestPoolResolve(t *testing.T) {
	dir := t.TempDir()
	content := "kubeconfig: \"\"\n"
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
	if _, err := cfg.Pools.Resolve("bogus"); err == nil {
		t.Fatal("expected error for unknown pool type")
	} else if !strings.Contains(err.Error(), "unknown pool type") {
		t.Errorf("error %q must mention unknown pool type", err.Error())
	}
}

func TestPoolOverrides(t *testing.T) {
	dir := t.TempDir()
	content := `
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
	content := "kubeconfig: \"\"\n"
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
	content := "theme: neon\n"
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
	content := "cluster: prod\ntheme: dark\n"
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
	content := "cluster: [unterminated\n"
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

func TestReservedDefaults(t *testing.T) {
	dir := t.TempDir()
	content := "kubeconfig: \"\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	r := cfg.Reserved
	if r.Token != (config.CredentialSource{}) {
		t.Errorf("Token = %+v, want empty default", r.Token)
	}
	if r.CloudInit != (config.CredentialSource{}) {
		t.Errorf("CloudInit = %+v, want empty default", r.CloudInit)
	}
	if r.Location != "hel1" || r.ServerType != "cpx22" {
		t.Errorf("location/type = %q/%q, want hel1/cpx22", r.Location, r.ServerType)
	}
	if r.Image.Label != "caph-image-name" {
		t.Errorf("image label = %q, want caph-image-name", r.Image.Label)
	}
	if r.Image.Value != "" {
		t.Errorf("image value = %q, want empty", r.Image.Value)
	}
	if len(r.SSHKeys) != 0 {
		t.Errorf("ssh keys = %v, want empty", r.SSHKeys)
	}
}

func TestReservedOverrides(t *testing.T) {
	dir := t.TempDir()
	content := `
reserved:
  token:
    env: HCLOUD_TOKEN
  cloud_init:
    path: ~/reserved-cloud-init.yaml
  location: nbg1
  server_type: cpx31
  image:
    label: my-image-label
    value: my-pool-node
  ssh_keys:
    - alpha
    - beta
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HORIZON_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	r := cfg.Reserved
	if r.Token.Env != "HCLOUD_TOKEN" || r.Token.Value != "" || r.Token.Path != "" {
		t.Errorf("token override = %+v", r.Token)
	}
	if r.CloudInit.Path != "~/reserved-cloud-init.yaml" || r.CloudInit.Value != "" || r.CloudInit.Env != "" {
		t.Errorf("cloud_init override = %+v", r.CloudInit)
	}
	if r.Location != "nbg1" || r.ServerType != "cpx31" {
		t.Errorf("location/type = %q/%q", r.Location, r.ServerType)
	}
	if r.Image.Label != "my-image-label" || r.Image.Value != "my-pool-node" {
		t.Errorf("image = %+v, want my-image-label/my-pool-node", r.Image)
	}
	if len(r.SSHKeys) != 2 || r.SSHKeys[0] != "alpha" {
		t.Errorf("ssh keys = %v", r.SSHKeys)
	}
}

func TestCredentialSourceValueWinsOverPathEnvAndSecret(t *testing.T) {
	t.Setenv("HZ_CRED", "from-env")
	kc := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cred"},
		Data:       map[string][]byte{"token": []byte("from-secret")},
	})
	c := config.CredentialSource{
		Value:  "from-value",
		Path:   "/nonexistent",
		Env:    "HZ_CRED",
		Secret: &config.SecretRef{Namespace: "ns", Name: "cred", Key: "token"},
	}
	got, err := c.Resolve(context.Background(), kc)
	if err != nil || got != "from-value" {
		t.Fatalf("Resolve() = %q, %v; want from-value", got, err)
	}
}

func TestCredentialSourcePathTrimsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "token")
	if err := os.WriteFile(p, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HZ_CRED", "from-env")
	c := config.CredentialSource{Path: p, Env: "HZ_CRED"}
	got, err := c.Resolve(context.Background(), nil)
	if err != nil || got != "secret-token" {
		t.Fatalf("Resolve() = %q, %v; want secret-token", got, err)
	}
}

func TestCredentialSourceEnvUsedWhenValueAndPathEmpty(t *testing.T) {
	t.Setenv("HZ_CRED", "env-token")
	c := config.CredentialSource{Env: "HZ_CRED"}
	got, err := c.Resolve(context.Background(), nil)
	if err != nil || got != "env-token" {
		t.Fatalf("Resolve() = %q, %v; want env-token", got, err)
	}
}

func TestCredentialSourceSecretUsedWhenOthersEmpty(t *testing.T) {
	kc := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cred"},
		Data:       map[string][]byte{"token": []byte("secret-value")},
	})
	c := config.CredentialSource{Secret: &config.SecretRef{Namespace: "ns", Name: "cred", Key: "token"}}
	got, err := c.Resolve(context.Background(), kc)
	if err != nil || got != "secret-value" {
		t.Fatalf("Resolve() = %q, %v; want secret-value", got, err)
	}
}

func TestCredentialSourceSecretMissingKeyFailsFast(t *testing.T) {
	kc := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cred"},
		Data:       map[string][]byte{"other": []byte("x")},
	})
	c := config.CredentialSource{Secret: &config.SecretRef{Namespace: "ns", Name: "cred", Key: "token"}}
	if _, err := c.Resolve(context.Background(), kc); err == nil {
		t.Fatal("expected error when secret lacks the requested key")
	}
}

func TestCredentialSourceSecretMissingSecretFailsFast(t *testing.T) {
	kc := fake.NewSimpleClientset()
	c := config.CredentialSource{Secret: &config.SecretRef{Namespace: "ns", Name: "absent", Key: "token"}}
	if _, err := c.Resolve(context.Background(), kc); err == nil {
		t.Fatal("expected error when the secret is absent")
	}
}

func TestCredentialSourceSecretRequiresClient(t *testing.T) {
	c := config.CredentialSource{Secret: &config.SecretRef{Namespace: "ns", Name: "cred", Key: "token"}}
	if _, err := c.Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error when the secret source has no kubernetes client")
	}
}

func TestCredentialSourceEmptyFailsFast(t *testing.T) {
	if _, err := (config.CredentialSource{}).Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error when no credential source is set")
	}
}

func TestExpandUserPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"~", home},
		{"~/", home},
		{"~/bedrock", filepath.Join(home, "bedrock")},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"~bob/x", "~bob/x"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := config.ExpandUserPath(tc.in); got != tc.want {
			t.Errorf("ExpandUserPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
