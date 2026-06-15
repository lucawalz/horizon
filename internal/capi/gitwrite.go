package capi

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const (
	clusterAPIDir         = "cluster-api"
	infraDir              = "infrastructure"
	kustomizationFile     = "kustomization.yaml"
	poolManifestFile      = "machinedeployment.yaml"
	clusterManifestFile   = "cluster.yaml"
	kustomizationHeader   = "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n"
)

type Repo struct {
	Root string
}

func OpenRepo(root string) (*Repo, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("capi: repo root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("capi: repo root %q is not a directory", root)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil, fmt.Errorf("capi: repo root %q is not a git work tree: %w", root, err)
	}
	return &Repo{Root: root}, nil
}

func (r *Repo) clusterAPIPath(clusterDir string) (string, error) {
	path := filepath.Join(r.Root, "kubernetes", "clusters", clusterDir, infraDir, clusterAPIDir)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("capi: cluster-api dir %q: %w", path, err)
	}
	return path, nil
}

func (r *Repo) WritePool(clusterDir, name string, data []byte) (string, error) {
	return r.writeManifest(clusterDir, name, poolManifestFile, data)
}

func (r *Repo) WriteCluster(clusterDir, name string, data []byte) (string, error) {
	return r.writeManifest(clusterDir, name, clusterManifestFile, data)
}

func (r *Repo) writeManifest(clusterDir, name, file string, data []byte) (string, error) {
	base, err := r.clusterAPIPath(clusterDir)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("capi: mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("capi: write %q: %w", path, err)
	}
	if err := appendResource(filepath.Join(dir, kustomizationFile), file); err != nil {
		return "", err
	}
	return path, nil
}

func appendResource(kustomizationPath, resource string) error {
	existing, err := os.ReadFile(kustomizationPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("capi: read %q: %w", kustomizationPath, err)
	}
	if os.IsNotExist(err) {
		existing = []byte(kustomizationHeader)
	}
	entry := "  - " + resource
	for _, line := range bytes.Split(existing, []byte("\n")) {
		if string(bytes.TrimRight(line, " ")) == entry {
			return nil
		}
	}
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		existing = append(existing, '\n')
	}
	existing = append(existing, []byte(entry+"\n")...)
	if err := os.WriteFile(kustomizationPath, existing, 0o644); err != nil {
		return fmt.Errorf("capi: write %q: %w", kustomizationPath, err)
	}
	return nil
}
