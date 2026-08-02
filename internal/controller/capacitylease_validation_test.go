package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

func validLease(t *testing.T) *v1alpha1.CapacityLease {
	t.Helper()
	return &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: objectName(t)},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: "hetzner",
			Region:      "nbg1",
			Size:        "cx22",
			Replicas:    1,
			Duration:    metav1.Duration{Duration: time.Hour},
		},
	}
}

func TestCapacityLeaseAcceptsAValidSpec(t *testing.T) {
	c := apiServerClient(t)
	assertCreate(t, c, validLease(t), false)
}

func TestCapacityLeaseReplicasBounds(t *testing.T) {
	tests := []struct {
		name         string
		replicas     int32
		wantRejected bool
	}{
		{"zero", 0, true},
		{"one", 1, false},
		{"eight", 8, false},
		{"nine", 9, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			lease.Spec.Replicas = tc.replicas
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseDurationBounds(t *testing.T) {
	tests := []struct {
		name         string
		duration     time.Duration
		wantRejected bool
	}{
		{"four minutes", 4 * time.Minute, true},
		{"five minutes", 5 * time.Minute, false},
		{"eight hours", 8 * time.Hour, false},
		{"nine hours", 9 * time.Hour, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			lease.Spec.Duration = metav1.Duration{Duration: tc.duration}
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseRejectsEmptyStringFields(t *testing.T) {
	tests := []struct {
		name  string
		blank func(*v1alpha1.CapacityLeaseSpec)
	}{
		{"providerRef", func(s *v1alpha1.CapacityLeaseSpec) { s.ProviderRef = "" }},
		{"region", func(s *v1alpha1.CapacityLeaseSpec) { s.Region = "" }},
		{"size", func(s *v1alpha1.CapacityLeaseSpec) { s.Size = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			tc.blank(&lease.Spec)
			assertCreate(t, c, lease, true)
		})
	}
}
