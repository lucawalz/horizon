package capi_test

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
)

const flavorTemplate = `apiVersion: cluster.x-k8s.io/v1beta2
kind: Cluster
metadata:
  name: ${CLUSTER_NAME}
spec:
  topology:
    classRef:
      name: ${CLUSTER_CLASS}
    version: ${KUBERNETES_VERSION}
`

func TestRenderFlavorSubstitutes(t *testing.T) {
	vars := map[string]string{
		"CLUSTER_NAME":       "edge",
		"CLUSTER_CLASS":      "hetzner-k3s",
		"KUBERNETES_VERSION": "v1.35.2+k3s1",
	}
	out, err := capi.RenderFlavor([]byte(flavorTemplate), vars)
	if err != nil {
		t.Fatalf("RenderFlavor: %v", err)
	}
	rendered := string(out)
	for _, want := range []string{"name: edge", "name: hetzner-k3s", "version: v1.35.2+k3s1"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered flavor missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "${") {
		t.Errorf("rendered flavor still has unsubstituted variables:\n%s", rendered)
	}
}

func TestRenderFlavorReportsMissingVariables(t *testing.T) {
	_, err := capi.RenderFlavor([]byte(flavorTemplate), map[string]string{"CLUSTER_NAME": "edge"})
	if err == nil {
		t.Fatal("expected error for missing variables")
	}
	for _, name := range []string{"CLUSTER_CLASS", "KUBERNETES_VERSION"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q should list missing variable %q", err, name)
		}
	}
}
