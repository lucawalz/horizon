package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func topologyClusterSpec(name, namespace, version string) capi.ClusterSpec {
	return capi.ClusterSpec{
		Name:                 name,
		Namespace:            namespace,
		Class:                "hetzner-k3s",
		WorkerClass:          "default-worker",
		WorkerName:           name + "-workers",
		Version:              version,
		ControlPlaneReplicas: 1,
		WorkerReplicas:       2,
		Variables:            []capi.ClusterVariable{{Name: "machineType", Value: "cpx22"}},
	}
}

func clusterTestApp(cc *capi.Client) *core.App {
	return &core.App{
		Config:     &config.Config{Pools: testPoolDefaults()},
		CapiClient: cc,
	}
}

func TestApplyClusterLive(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(cc)
	spec := topologyClusterSpec("edge", "caph-system", "v1.31.0+k3s1")

	if err := core.ApplyCluster(context.Background(), app, spec, core.Progress{}); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}

	got, err := cc.GetCluster(context.Background(), "caph-system", "edge")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.Spec.Topology.ClassRef.Name != "hetzner-k3s" {
		t.Errorf("topology classRef name = %q, want hetzner-k3s", got.Spec.Topology.ClassRef.Name)
	}
}

func TestRenderClusterEmitsTopology(t *testing.T) {
	spec := topologyClusterSpec("edge", "caph-system", "v1.35.2+k3s1")
	out, err := core.RenderCluster(spec)
	if err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	rendered := string(out)
	for _, want := range []string{"kind: Cluster", "classRef:", "hetzner-k3s", "v1.35.2+k3s1"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered manifests missing %q:\n%s", want, rendered)
		}
	}
	for _, forbidden := range []string{"HetznerCluster", "HCloudMachineTemplate", "KThreesControlPlane"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("rendered manifests must not contain provider kind %q:\n%s", forbidden, rendered)
		}
	}
}

func TestRenderClusterDoesNotApply(t *testing.T) {
	cc := capiClient(t)
	spec := topologyClusterSpec("edge", "caph-system", "v1.31.0+k3s1")

	if _, err := core.RenderCluster(spec); err != nil {
		t.Fatalf("RenderCluster: %v", err)
	}
	if _, err := cc.GetCluster(context.Background(), "caph-system", "edge"); !apierrors.IsNotFound(err) {
		t.Errorf("render must not apply; GetCluster = %v, want NotFound", err)
	}
}

func TestWriteClusterManifestsToRepo(t *testing.T) {
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
	app := clusterTestApp(cc)
	app.Config.RepoPath = root
	spec := topologyClusterSpec("edge", "caph-system", "v1.31.0+k3s1")

	path, err := core.WriteClusterManifests(app, spec, core.Progress{})
	if err != nil {
		t.Fatalf("WriteClusterManifests: %v", err)
	}

	written := filepath.Join(clusterDir, "edge", "cluster.yaml")
	if path != written {
		t.Errorf("returned path %q, want %q", path, written)
	}
	if _, err := os.Stat(written); err != nil {
		t.Errorf("expected manifest at %q: %v", written, err)
	}
	if _, err := cc.GetCluster(context.Background(), "caph-system", "edge"); !apierrors.IsNotFound(err) {
		t.Errorf("write must not apply live; GetCluster = %v, want NotFound", err)
	}
}

func TestWriteClusterManifestsRequiresRepoPath(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(cc)
	spec := topologyClusterSpec("edge", "caph-system", "v1.31.0+k3s1")

	if _, err := core.WriteClusterManifests(app, spec, core.Progress{}); err == nil {
		t.Fatal("expected error when repo_path unset")
	}
}

func TestDeleteClusterRemovesCluster(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(cc)
	spec := topologyClusterSpec("edge", "caph-system", "v1.31.0+k3s1")
	if err := core.ApplyCluster(context.Background(), app, spec, core.Progress{}); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}

	if err := core.DeleteCluster(context.Background(), app, "caph-system", "edge", core.Progress{}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, err := cc.GetCluster(context.Background(), "caph-system", "edge"); !apierrors.IsNotFound(err) {
		t.Errorf("GetCluster after delete = %v, want NotFound", err)
	}
}

func TestListClustersReturnsCreated(t *testing.T) {
	cc := capiClient(t)
	app := clusterTestApp(cc)
	spec := topologyClusterSpec("edge", "caph-system", "v1.31.0+k3s1")
	if err := core.ApplyCluster(context.Background(), app, spec, core.Progress{}); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}

	clusters, err := core.ListClusters(context.Background(), app, "caph-system")
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	found := false
	for i := range clusters {
		if clusters[i].Name == "edge" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListClusters did not return created cluster edge: %+v", clusters)
	}
}

func TestRenderFlavorMissingVariableFails(t *testing.T) {
	template := []byte("kind: Cluster\nmetadata:\n  name: ${CLUSTER_NAME}\n")
	if _, err := core.RenderFlavor(template, map[string]string{}); err == nil {
		t.Fatal("expected error for missing flavor variable")
	}
}

func TestRenderFlavorSubstitutes(t *testing.T) {
	template := []byte("kind: Cluster\nmetadata:\n  name: ${CLUSTER_NAME}\n")
	out, err := core.RenderFlavor(template, map[string]string{"CLUSTER_NAME": "edge"})
	if err != nil {
		t.Fatalf("RenderFlavor: %v", err)
	}
	if !strings.Contains(string(out), "name: edge") {
		t.Errorf("flavor not substituted:\n%s", out)
	}
}
