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

func ApplyCluster(ctx context.Context, app *App, spec capi.ClusterSpec) error {
	return app.CapiClient.ApplyCluster(ctx, spec)
}

func WriteClusterManifests(app *App, spec capi.ClusterSpec) (string, error) {
	data, err := capi.RenderCluster(spec)
	if err != nil {
		return "", err
	}
	return writeClusterTree(app, spec.Name, data)
}

func RenderFlavor(template []byte, vars map[string]string) ([]byte, error) {
	return capi.RenderFlavor(template, vars)
}

func ApplyFlavor(ctx context.Context, app *App, template []byte, vars map[string]string) error {
	data, err := capi.RenderFlavor(template, vars)
	if err != nil {
		return err
	}
	return app.CapiClient.ApplyManifests(ctx, data)
}

func WriteFlavorManifests(app *App, name string, template []byte, vars map[string]string) (string, error) {
	data, err := capi.RenderFlavor(template, vars)
	if err != nil {
		return "", err
	}
	return writeClusterTree(app, name, data)
}

func writeClusterTree(app *App, name string, data []byte) (string, error) {
	if app.Config.BedrockPath == "" {
		return "", fmt.Errorf("--write requires bedrock_path in config")
	}
	repo, err := capi.OpenRepo(app.Config.BedrockPath)
	if err != nil {
		return "", err
	}
	return repo.WriteCluster(name, name, data)
}

func DeleteCluster(ctx context.Context, app *App, namespace, name string) error {
	return app.CapiClient.DeleteCluster(ctx, namespace, name)
}

func ListClusters(ctx context.Context, app *App, namespace string) ([]clusterv1.Cluster, error) {
	return app.CapiClient.ListClusters(ctx, namespace)
}
