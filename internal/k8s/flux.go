package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	kustomizationGVR = schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}
	helmReleaseGVR   = schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}
)

type FluxResource struct {
	Name  string
	Ready bool
}

type FluxClient struct {
	dyn dynamic.Interface
}

func NewFluxClient(kubeconfigPath, contextName string) (*FluxClient, error) {
	restCfg, err := RestConfigForContext(kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	return &FluxClient{dyn: dyn}, nil
}

func NewFluxClientWithDynamic(dyn dynamic.Interface) *FluxClient {
	return &FluxClient{dyn: dyn}
}

func (c *FluxClient) ListKustomizations(ctx context.Context) ([]FluxResource, error) {
	return c.listFlux(ctx, kustomizationGVR)
}

func (c *FluxClient) ListHelmReleases(ctx context.Context) ([]FluxResource, error) {
	return c.listFlux(ctx, helmReleaseGVR)
}

func (c *FluxClient) listFlux(ctx context.Context, gvr schema.GroupVersionResource) ([]FluxResource, error) {
	list, err := c.dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	out := make([]FluxResource, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, FluxResource{
			Name:  list.Items[i].GetName(),
			Ready: fluxReady(&list.Items[i]),
		})
	}
	return out, nil
}

func fluxReady(obj *unstructured.Unstructured) bool {
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
