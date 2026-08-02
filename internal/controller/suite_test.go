package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	envtestAssetsVar = "KUBEBUILDER_ASSETS"
	envtestBinRoot   = "../../bin/k8s"
	crdBasesDir      = "../../config/crd/bases"
	envtestMissing   = "envtest binaries not installed; run `make envtest` or set " + envtestAssetsVar
)

var (
	testClient client.Client
	skipReason string
)

func TestMain(m *testing.M) {
	binDir, ok := envtestBinaryDir()
	if !ok {
		skipReason = envtestMissing
		os.Exit(m.Run())
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdBasesDir},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: binDir,
	}

	restConfig, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest: %v\n", err)
		os.Exit(1)
	}

	testClient, err = client.New(restConfig, client.Options{Scheme: testScheme()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "build client: %v\n", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
	}
	os.Exit(code)
}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

func envtestBinaryDir() (string, bool) {
	if os.Getenv(envtestAssetsVar) != "" {
		return "", true
	}
	entries, err := os.ReadDir(envtestBinRoot)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(envtestBinRoot, entry.Name()), true
		}
	}
	return "", false
}

func apiServerClient(t *testing.T) client.Client {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	return testClient
}

var nonNameCharacters = regexp.MustCompile(`[^a-z0-9]+`)

func objectName(t *testing.T) string {
	t.Helper()
	return strings.Trim(nonNameCharacters.ReplaceAllString(strings.ToLower(t.Name()), "-"), "-")
}

func assertCreate(t *testing.T, c client.Client, obj client.Object, wantRejected bool) {
	t.Helper()
	err := c.Create(t.Context(), obj)
	if err == nil {
		t.Cleanup(func() { _ = c.Delete(context.Background(), obj) })
	}
	switch {
	case wantRejected && err == nil:
		t.Fatalf("apiserver accepted %T, want rejection", obj)
	case !wantRejected && err != nil:
		t.Fatalf("apiserver rejected %T: %v", obj, err)
	}
}
