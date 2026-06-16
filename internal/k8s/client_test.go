package k8s_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
)

const twoContextKubeconfig = `apiVersion: v1
kind: Config
current-context: alpha
clusters:
- name: alpha-cluster
  cluster:
    server: https://alpha.example.com:6443
    insecure-skip-tls-verify: true
- name: beta-cluster
  cluster:
    server: https://beta.example.com:6443
    insecure-skip-tls-verify: true
contexts:
- name: alpha
  context:
    cluster: alpha-cluster
    user: alpha-user
- name: beta
  context:
    cluster: beta-cluster
    user: beta-user
users:
- name: alpha-user
  user:
    token: alpha-token
- name: beta-user
  user:
    token: beta-token
`

func writeKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func TestRestConfigForContextEmptyMatchesRestConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	path := writeKubeconfig(t)

	plain, err := k8s.RestConfig(path)
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	forCtx, err := k8s.RestConfigForContext(path, "")
	if err != nil {
		t.Fatalf("RestConfigForContext: %v", err)
	}
	if plain.Host != forCtx.Host {
		t.Errorf("host mismatch: RestConfig=%q RestConfigForContext=%q", plain.Host, forCtx.Host)
	}
	if forCtx.Host != "https://alpha.example.com:6443" {
		t.Errorf("default context host = %q, want alpha", forCtx.Host)
	}
}

func TestRestConfigForContextSelectsContext(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	path := writeKubeconfig(t)

	cfg, err := k8s.RestConfigForContext(path, "beta")
	if err != nil {
		t.Fatalf("RestConfigForContext: %v", err)
	}
	if cfg.Host != "https://beta.example.com:6443" {
		t.Errorf("host = %q, want beta server", cfg.Host)
	}
}

func TestRestConfigForContextUnknownContextFails(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	path := writeKubeconfig(t)

	if _, err := k8s.RestConfigForContext(path, "missing"); err == nil {
		t.Fatal("expected error for unknown context")
	}
}

func TestContexts(t *testing.T) {
	path := writeKubeconfig(t)

	names, current, err := k8s.Contexts(path)
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	if current != "alpha" {
		t.Errorf("current = %q, want alpha", current)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("names = %v, want [alpha beta]", names)
	}
}

func TestInCluster(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
		if !k8s.InCluster() {
			t.Fatal("expected InCluster() true when KUBERNETES_SERVICE_HOST is set")
		}
	})
	t.Run("unset", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		if k8s.InCluster() {
			t.Fatal("expected InCluster() false when KUBERNETES_SERVICE_HOST is empty")
		}
	})
}
