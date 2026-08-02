package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

func secretKeyRef(name, key string) corev1.SecretKeySelector {
	return corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: name},
		Key:                  key,
	}
}

func hetznerBlock() *v1alpha1.HetznerProviderSpec {
	return &v1alpha1.HetznerProviderSpec{
		CredentialsSecretRef: secretKeyRef("hcloud", "token"),
		CloudInitSecretRef:   secretKeyRef("cloud-init", "user-data"),
	}
}

func validProviderConfig(t *testing.T) *v1alpha1.ProviderConfig {
	t.Helper()
	return &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: objectName(t)},
		Spec: v1alpha1.ProviderConfigSpec{
			Type:    v1alpha1.ProviderTypeHetzner,
			Hetzner: hetznerBlock(),
			Watchdog: v1alpha1.WatchdogPolicy{
				RenewInterval: metav1.Duration{Duration: time.Minute},
				Slack:         metav1.Duration{Duration: 2 * time.Minute},
				MaxLifetime:   metav1.Duration{Duration: 8 * time.Hour},
			},
		},
	}
}

func TestProviderConfigProviderBlockMustMatchType(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*v1alpha1.ProviderConfigSpec)
		wantRejected bool
	}{
		{"matching type and block", func(*v1alpha1.ProviderConfigSpec) {}, false},
		{"type without block", func(s *v1alpha1.ProviderConfigSpec) { s.Hetzner = nil }, true},
		{"unknown type with block", func(s *v1alpha1.ProviderConfigSpec) { s.Type = "aws" }, true},
		{"unknown type without block", func(s *v1alpha1.ProviderConfigSpec) {
			s.Type = "aws"
			s.Hetzner = nil
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			config := validProviderConfig(t)
			tc.mutate(&config.Spec)
			assertCreate(t, c, config, tc.wantRejected)
		})
	}
}
