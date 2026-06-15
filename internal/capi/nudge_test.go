package capi_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func clusterObject(name string, initialized *bool) *clusterv1.Cluster {
	return &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "caph-system"},
		Status: clusterv1.ClusterStatus{
			Initialization: clusterv1.ClusterInitializationStatus{
				ControlPlaneInitialized: initialized,
			},
		},
	}
}

func TestIsControlPlaneInitialized(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		init *bool
		want bool
	}{
		{"nil", nil, false},
		{"true", &yes, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(mustScheme(t)).
				WithObjects(clusterObject("burst", tc.init)).
				WithStatusSubresource(&clusterv1.Cluster{}).
				Build()
			c := capi.NewClientWithCRClient(cl)
			got, err := c.IsControlPlaneInitialized(context.Background(), "caph-system", "burst")
			if err != nil {
				t.Fatalf("IsControlPlaneInitialized: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNudgeControlPlaneInitialized(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithScheme(mustScheme(t)).
		WithObjects(clusterObject("burst", nil)).
		WithStatusSubresource(&clusterv1.Cluster{}).
		Build()
	c := capi.NewClientWithCRClient(cl)

	if err := c.NudgeControlPlaneInitialized(context.Background(), "caph-system", "burst"); err != nil {
		t.Fatalf("Nudge: %v", err)
	}
	got, err := c.IsControlPlaneInitialized(context.Background(), "caph-system", "burst")
	if err != nil {
		t.Fatalf("IsControlPlaneInitialized: %v", err)
	}
	if !got {
		t.Error("control plane not marked initialized after nudge")
	}
}
