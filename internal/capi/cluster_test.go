package capi_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetClusterNotFound(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	if _, err := c.GetCluster(context.Background(), "caph-system", "missing"); !apierrors.IsNotFound(err) {
		t.Errorf("GetCluster on missing cluster = %v, want NotFound", err)
	}
}

func TestApplyManifestsCreatesAndUpdates(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(mustScheme(t)).Build()
	c := capi.NewClientWithCRClient(cl)

	data, err := capi.RenderPool(testSpec())
	if err != nil {
		t.Fatalf("RenderPool: %v", err)
	}
	if err := c.ApplyManifests(context.Background(), data); err != nil {
		t.Fatalf("ApplyManifests create: %v", err)
	}
	if err := c.ApplyManifests(context.Background(), data); err != nil {
		t.Fatalf("ApplyManifests update: %v", err)
	}
}
