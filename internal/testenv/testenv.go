// Package testenv brings up a shared envtest control plane for the integration suites.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	assetsVar   = "KUBEBUILDER_ASSETS"
	binRoot     = "../../bin/k8s"
	crdBasesDir = "../../config/crd/bases"
	missing     = "envtest binaries not installed; run `make envtest` or set " + assetsVar
)

type Environment struct {
	Config     *rest.Config
	Client     client.Client
	SkipReason string

	control *envtest.Environment
}

func Start(scheme *runtime.Scheme) (*Environment, error) {
	binDir, installed := binaryDir()
	if !installed {
		return &Environment{SkipReason: missing}, nil
	}

	control := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdBasesDir},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: binDir,
	}
	restConfig, err := control.Start()
	if err != nil {
		return nil, fmt.Errorf("start envtest: %w", err)
	}

	apiClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		_ = control.Stop()
		return nil, fmt.Errorf("build client: %w", err)
	}
	return &Environment{Config: restConfig, Client: apiClient, control: control}, nil
}

func (e *Environment) Stop() error {
	if e.control == nil {
		return nil
	}
	return e.control.Stop()
}

func (e *Environment) SkipUnlessRunning(t *testing.T) {
	t.Helper()
	if e.SkipReason != "" {
		t.Skip(e.SkipReason)
	}
}

func binaryDir() (string, bool) {
	if os.Getenv(assetsVar) != "" {
		return "", true
	}
	entries, err := os.ReadDir(binRoot)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(binRoot, entry.Name()), true
		}
	}
	return "", false
}
