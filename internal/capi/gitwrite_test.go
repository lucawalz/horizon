package capi_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
)

func fakeBedrock(t *testing.T, clusterDir string, withKustomization bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	capiDir := filepath.Join(root, "kubernetes", "clusters", clusterDir, "infrastructure", "cluster-api")
	if err := os.MkdirAll(capiDir, 0o755); err != nil {
		t.Fatalf("mkdir cluster-api: %v", err)
	}
	if withKustomization {
		stub := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n"
		if err := os.WriteFile(filepath.Join(capiDir, "kustomization.yaml"), []byte(stub), 0o644); err != nil {
			t.Fatalf("write stub kustomization: %v", err)
		}
	}
	return root
}

func TestOpenRepoRejectsNonGit(t *testing.T) {
	dir := t.TempDir()
	if _, err := capi.OpenRepo(dir); err == nil {
		t.Fatal("OpenRepo on non-git dir: want error, got nil")
	}
}

func TestWritePoolWritesAndAppends(t *testing.T) {
	root := fakeBedrock(t, "home", true)
	repo, err := capi.OpenRepo(root)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	path, err := repo.WritePool("home", "burst", []byte("pool: data\n"))
	if err != nil {
		t.Fatalf("WritePool: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("written file missing: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("burst", "machinedeployment.yaml")) {
		t.Errorf("path = %q, want .../burst/machinedeployment.yaml", path)
	}

	kpath := filepath.Join(filepath.Dir(path), "kustomization.yaml")
	body, err := os.ReadFile(kpath)
	if err != nil {
		t.Fatalf("read kustomization: %v", err)
	}
	if strings.Count(string(body), "machinedeployment.yaml") != 1 {
		t.Errorf("resource entry count = %d, want 1\n%s", strings.Count(string(body), "machinedeployment.yaml"), body)
	}

	if _, err := repo.WritePool("home", "burst", []byte("pool: data2\n")); err != nil {
		t.Fatalf("WritePool second: %v", err)
	}
	body2, err := os.ReadFile(kpath)
	if err != nil {
		t.Fatalf("read kustomization 2: %v", err)
	}
	if strings.Count(string(body2), "machinedeployment.yaml") != 1 {
		t.Errorf("idempotency broken: resource count = %d, want 1\n%s", strings.Count(string(body2), "machinedeployment.yaml"), body2)
	}
}

func TestWriteClusterCreatesKustomization(t *testing.T) {
	root := fakeBedrock(t, "home", false)
	repo, err := capi.OpenRepo(root)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	path, err := repo.WriteCluster("home", "burst", []byte("cluster: data\n"))
	if err != nil {
		t.Fatalf("WriteCluster: %v", err)
	}
	kpath := filepath.Join(filepath.Dir(path), "kustomization.yaml")
	body, err := os.ReadFile(kpath)
	if err != nil {
		t.Fatalf("read kustomization: %v", err)
	}
	if !strings.Contains(string(body), "kind: Kustomization") {
		t.Errorf("created kustomization missing header:\n%s", body)
	}
	if !strings.Contains(string(body), "cluster.yaml") {
		t.Errorf("cluster.yaml not in resources:\n%s", body)
	}
}
