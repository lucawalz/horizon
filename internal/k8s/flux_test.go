package k8s_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var fluxListKinds = map[schema.GroupVersionResource]string{
	{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}: "KustomizationList",
	{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}:        "HelmReleaseList",
}

func fluxObject(apiVersion, kind, name string, ready bool) *unstructured.Unstructured {
	status := "False"
	if ready {
		status = "True"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": "flux-system"},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": status}},
		},
	}}
}

func fluxClient(objs ...runtime.Object) *k8s.FluxClient {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), fluxListKinds, objs...)
	return k8s.NewFluxClientWithDynamic(dyn)
}

func TestListKustomizationsReportsReadiness(t *testing.T) {
	c := fluxClient(
		fluxObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "infra", true),
		fluxObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "apps", false),
	)

	got, err := c.ListKustomizations(context.Background())
	if err != nil {
		t.Fatalf("ListKustomizations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	ready := map[string]bool{}
	for _, r := range got {
		ready[r.Name] = r.Ready
	}
	if !ready["infra"] || ready["apps"] {
		t.Errorf("readiness = %v, want infra ready and apps not ready", ready)
	}
}

func TestListHelmReleasesReportsReadiness(t *testing.T) {
	c := fluxClient(fluxObject("helm.toolkit.fluxcd.io/v2", "HelmRelease", "traefik", true))

	got, err := c.ListHelmReleases(context.Background())
	if err != nil {
		t.Fatalf("ListHelmReleases: %v", err)
	}
	if len(got) != 1 || !got[0].Ready || got[0].Name != "traefik" {
		t.Errorf("got %+v, want a single ready traefik release", got)
	}
}

func TestListToleratesAbsentFluxCRDs(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), fluxListKinds)
	dyn.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		resource := action.GetResource()
		return true, nil, apierrors.NewNotFound(resource.GroupResource(), "")
	})
	c := k8s.NewFluxClientWithDynamic(dyn)

	kustomizations, err := c.ListKustomizations(context.Background())
	if err != nil {
		t.Fatalf("absent CRDs must not error, got: %v", err)
	}
	if len(kustomizations) != 0 {
		t.Errorf("kustomizations = %+v, want empty", kustomizations)
	}

	helmReleases, err := c.ListHelmReleases(context.Background())
	if err != nil {
		t.Fatalf("absent CRDs must not error, got: %v", err)
	}
	if len(helmReleases) != 0 {
		t.Errorf("helmreleases = %+v, want empty", helmReleases)
	}
}
