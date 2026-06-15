package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func clusterTestApp(t *testing.T, cc *capi.Client) *cli.App {
	t.Helper()
	return &cli.App{
		Config: &config.Config{
			Pools: testPoolDefaults(),
		},
		CapiClient: cc,
	}
}

func TestClusterCreateLiveApply(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(t, cc)
	cmd := cli.NewClusterCmdForTest(app)
	cmd.SetArgs([]string{"create", "--name", "edge", "--namespace", "caph-system", "--version", "v1.31.0+k3s1"})
	cmd.PersistentFlags().Bool("dry-run", false, "")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cluster create: %v", err)
	}

	got, err := cc.GetCluster(context.Background(), "caph-system", "edge")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.Spec.ControlPlaneRef.Kind != "KThreesControlPlane" {
		t.Errorf("controlPlaneRef kind = %q, want managed control plane", got.Spec.ControlPlaneRef.Kind)
	}
}

func TestClusterCreateDryRunWritesNothing(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(t, cc)
	cmd := cli.NewClusterCmdForTest(app)
	cmd.SetArgs([]string{"create", "--name", "edge", "--version", "v1.31.0+k3s1", "--dry-run"})
	cmd.PersistentFlags().Bool("dry-run", true, "")

	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("cluster create dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "kind: Cluster") {
		t.Errorf("dry-run output missing rendered cluster YAML:\n%s", out)
	}
	if !strings.Contains(out, "kind: HetznerCluster") {
		t.Errorf("dry-run cluster infrastructureRef must reference HetznerCluster:\n%s", out)
	}
	if _, err := cc.GetCluster(context.Background(), "caph-system", "edge"); !apierrors.IsNotFound(err) {
		t.Errorf("dry-run must not apply; GetCluster = %v, want NotFound", err)
	}
}

func TestClusterCreateWriteToBedrock(t *testing.T) {
	root := t.TempDir()
	clusterDir := filepath.Join(root, "kubernetes", "clusters", "edge", "infrastructure", "cluster-api")
	if err := os.MkdirAll(clusterDir, 0o755); err != nil {
		t.Fatalf("mkdir cluster-api: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterDir, "kustomization.yaml"), []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n"), 0o644); err != nil {
		t.Fatalf("write kustomization: %v", err)
	}

	cc := capiClient(t)
	app := clusterTestApp(t, cc)
	app.Config.BedrockPath = root

	cmd := cli.NewClusterCmdForTest(app)
	cmd.SetArgs([]string{"create", "--name", "edge", "--version", "v1.31.0+k3s1", "--write"})
	cmd.PersistentFlags().Bool("dry-run", false, "")

	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("cluster create --write: %v", err)
		}
	})

	written := filepath.Join(clusterDir, "edge", "cluster.yaml")
	if !strings.Contains(out, written) {
		t.Errorf("output %q should report written path %q", out, written)
	}
	if _, err := os.Stat(written); err != nil {
		t.Errorf("expected manifest at %q: %v", written, err)
	}
	if _, err := cc.GetCluster(context.Background(), "caph-system", "edge"); !apierrors.IsNotFound(err) {
		t.Errorf("--write must not apply live; GetCluster = %v, want NotFound", err)
	}
}

func TestClusterDeleteRemovesCluster(t *testing.T) {
	cc := capiClient(t)
	spec := capi.ClusterSpec{
		Name: "edge", Namespace: "caph-system", ClusterName: "edge",
		ControlPlaneMode: capi.External, PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16",
		Version: "v1.31.0+k3s1", Replicas: 1,
		ClusterInfrastructure: capi.TemplateRef{APIGroup: "infrastructure.cluster.x-k8s.io", Kind: "HetznerCluster", Name: "edge"},
		Infrastructure:        capi.TemplateRef{APIGroup: "infrastructure.cluster.x-k8s.io", Kind: "HCloudMachineTemplate", Name: "edge-workers"},
		Bootstrap:             capi.TemplateRef{APIGroup: "bootstrap.cluster.x-k8s.io", Kind: "KThreesConfigTemplate", Name: "edge"},
	}
	if err := cc.ApplyCluster(context.Background(), spec); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}

	app := clusterTestApp(t, cc)
	cmd := cli.NewClusterCmdForTest(app)
	cmd.SetArgs([]string{"delete", "--name", "edge", "--namespace", "caph-system"})
	cmd.PersistentFlags().Bool("dry-run", false, "")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cluster delete: %v", err)
	}
	if _, err := cc.GetCluster(context.Background(), "caph-system", "edge"); !apierrors.IsNotFound(err) {
		t.Errorf("GetCluster after delete = %v, want NotFound", err)
	}
}

func TestClusterListRendersCreated(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(t, cc)

	create := cli.NewClusterCmdForTest(app)
	create.SetArgs([]string{"create", "--name", "edge", "--version", "v1.31.0+k3s1"})
	create.PersistentFlags().Bool("dry-run", false, "")
	if err := create.Execute(); err != nil {
		t.Fatalf("cluster create: %v", err)
	}

	list := cli.NewClusterCmdForTest(app)
	list.SetArgs([]string{"list", "--namespace", "caph-system"})
	list.PersistentFlags().Bool("dry-run", false, "")
	out := captureStdout(func() {
		if err := list.Execute(); err != nil {
			t.Errorf("cluster list: %v", err)
		}
	})
	if !strings.Contains(out, "edge") {
		t.Errorf("list output missing created cluster:\n%s", out)
	}
}
