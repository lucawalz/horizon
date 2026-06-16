package core

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func RenderCluster(spec capi.ClusterSpec) ([]byte, error) {
	out, err := capi.RenderCluster(spec)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func ApplyCluster(ctx context.Context, app *App, spec capi.ClusterSpec) error {
	return app.CapiClient.ApplyCluster(ctx, spec)
}

func WriteClusterManifests(app *App, spec capi.ClusterSpec) (string, error) {
	if app.Config.BedrockPath == "" {
		return "", fmt.Errorf("--write requires bedrock_path in config")
	}
	data, err := capi.RenderCluster(spec)
	if err != nil {
		return "", err
	}
	repo, err := capi.OpenRepo(app.Config.BedrockPath)
	if err != nil {
		return "", err
	}
	return repo.WriteCluster(spec.ClusterName, spec.Name, data)
}

func DeleteCluster(ctx context.Context, app *App, namespace, name string) error {
	return app.CapiClient.DeleteCluster(ctx, namespace, name)
}

func ListClusters(ctx context.Context, app *App, namespace string) ([]clusterv1.Cluster, error) {
	return app.CapiClient.ListClusters(ctx, namespace)
}
