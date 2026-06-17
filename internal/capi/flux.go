package capi

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	kustomizationGVK = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "KustomizationList"}
	helmReleaseGVK   = schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmReleaseList"}
)

type FluxResource struct {
	Name  string
	Ready bool
}

func (c *Client) ListKustomizations(ctx context.Context) ([]FluxResource, error) {
	return c.listFlux(ctx, kustomizationGVK)
}

func (c *Client) ListHelmReleases(ctx context.Context) ([]FluxResource, error) {
	return c.listFlux(ctx, helmReleaseGVK)
}

func (c *Client) listFlux(ctx context.Context, gvk schema.GroupVersionKind) ([]FluxResource, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	if err := c.crClient().List(ctx, list); err != nil {
		return nil, fmt.Errorf("capi: list %s: %w", gvk.Kind, err)
	}
	out := make([]FluxResource, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, FluxResource{
			Name:  list.Items[i].GetName(),
			Ready: readyCondition(&list.Items[i]),
		})
	}
	return out, nil
}

func readyCondition(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] == "Ready" {
			return cond["status"] == "True"
		}
	}
	return false
}
