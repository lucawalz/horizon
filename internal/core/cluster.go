package core

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func RenderCluster(spec capi.ClusterSpec) ([]byte, error) {
	return capi.RenderCluster(spec)
}

func ApplyCluster(ctx context.Context, app *App, spec capi.ClusterSpec, progress Progress) error {
	progress.Debug(fmt.Sprintf("apply cluster %s/%s", spec.Namespace, spec.Name))
	return app.CapiClient.ApplyCluster(ctx, spec)
}

func WriteClusterManifests(app *App, spec capi.ClusterSpec, progress Progress) (string, error) {
	data, err := capi.RenderCluster(spec)
	if err != nil {
		return "", err
	}
	progress.Debug(fmt.Sprintf("write cluster %s/%s manifests", spec.Namespace, spec.Name))
	return writeClusterTree(app, spec.Name, data)
}

func RenderFlavor(template []byte, vars map[string]string) ([]byte, error) {
	return capi.RenderFlavor(template, vars)
}

func ApplyFlavor(ctx context.Context, app *App, template []byte, vars map[string]string, progress Progress) error {
	data, err := capi.RenderFlavor(template, vars)
	if err != nil {
		return err
	}
	progress.Debug("apply flavor manifests")
	return app.CapiClient.ApplyManifests(ctx, data)
}

func WriteFlavorManifests(app *App, name string, template []byte, vars map[string]string, progress Progress) (string, error) {
	data, err := capi.RenderFlavor(template, vars)
	if err != nil {
		return "", err
	}
	progress.Debug("write flavor " + name + " manifests")
	return writeClusterTree(app, name, data)
}

func writeClusterTree(app *App, name string, data []byte) (string, error) {
	if app.Config.RepoPath == "" {
		return "", fmt.Errorf("--write requires repo_path in config")
	}
	repo, err := capi.OpenRepo(app.Config.RepoPath)
	if err != nil {
		return "", err
	}
	return repo.WriteCluster(name, name, data)
}

func DeleteCluster(ctx context.Context, app *App, namespace, name string, progress Progress) error {
	progress.Debug(fmt.Sprintf("delete cluster %s/%s", namespace, name))
	return app.CapiClient.DeleteCluster(ctx, namespace, name)
}

func ListClusters(ctx context.Context, app *App, namespace string) ([]clusterv1.Cluster, error) {
	return app.CapiClient.ListClusters(ctx, namespace)
}
