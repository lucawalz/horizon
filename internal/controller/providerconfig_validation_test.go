package controller

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

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
		ImageSelector:        map[string]string{"caph-image-name": "bedrock-cluster-node"},
	}
}

func validProviderConfig(name string) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
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
			config := validProviderConfig(objectName(t))
			tc.mutate(&config.Spec)
			assertCreate(t, c, config, tc.wantRejected)
		})
	}
}

func TestProviderConfigWatchdogBounds(t *testing.T) {
	tests := []struct {
		name          string
		renewInterval time.Duration
		slack         time.Duration
		maxLifetime   time.Duration
		wantRejected  bool
	}{
		{"nominal policy", time.Minute, 2 * time.Minute, 8 * time.Hour, false},
		{"renew interval below floor", 9 * time.Second, 30 * time.Second, 5 * time.Minute, true},
		{"renew interval at floor", 10 * time.Second, 30 * time.Second, 5 * time.Minute, false},
		{"renew interval zero", 0, 30 * time.Second, 5 * time.Minute, true},
		{"renew interval negative", -time.Minute, 30 * time.Second, 5 * time.Minute, true},
		{"slack equal to renew interval", time.Minute, time.Minute, 8 * time.Hour, true},
		{"slack below renew interval", time.Minute, 30 * time.Second, 8 * time.Hour, true},
		{"slack one second above renew interval", time.Minute, time.Minute + time.Second, 8 * time.Hour, false},
		{"renew interval plus slack at one hour", 20 * time.Minute, 40 * time.Minute, 8 * time.Hour, false},
		{"renew interval plus slack above one hour", 20 * time.Minute, 40*time.Minute + time.Second, 8 * time.Hour, true},
		{"max lifetime below floor", 10 * time.Second, 30 * time.Second, 5*time.Minute - time.Second, true},
		{"max lifetime at floor", 10 * time.Second, 30 * time.Second, 5 * time.Minute, false},
		{"max lifetime at ceiling", time.Minute, 2 * time.Minute, 24 * time.Hour, false},
		{"max lifetime above ceiling", time.Minute, 2 * time.Minute, 24*time.Hour + time.Second, true},
		{"max lifetime equal to renew interval plus slack", 10 * time.Second, 5 * time.Minute, 5*time.Minute + 10*time.Second, true},
		{"max lifetime just above renew interval plus slack", 10 * time.Second, 5 * time.Minute, 5*time.Minute + 11*time.Second, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			config := validProviderConfig(objectName(t))
			config.Spec.Watchdog = v1alpha1.WatchdogPolicy{
				RenewInterval: metav1.Duration{Duration: tc.renewInterval},
				Slack:         metav1.Duration{Duration: tc.slack},
				MaxLifetime:   metav1.Duration{Duration: tc.maxLifetime},
			}
			assertCreate(t, c, config, tc.wantRejected)
		})
	}
}

func TestProviderConfigWatchdogRejectsUnparseableDurations(t *testing.T) {
	fields := []string{"renewInterval", "slack", "maxLifetime"}
	values := []string{"1d", "5 minutes", "", "30"}

	for _, field := range fields {
		for _, value := range values {
			t.Run(field+"/"+value, func(t *testing.T) {
				c := apiServerClient(t)
				assertCreate(t, c, rawWatchdogProviderConfig(t, field, value), true)
			})
		}
	}
}

func TestProviderConfigImageValidation(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*v1alpha1.HetznerProviderSpec)
		wantRejected bool
	}{
		{"legacy selector alone", func(h *v1alpha1.HetznerProviderSpec) {
			h.ImageSelector = map[string]string{"caph-image-name": "bedrock-cluster-node"}
		}, false},
		{"image name alone", func(h *v1alpha1.HetznerProviderSpec) {
			h.ImageSelector = nil
			h.Image = &v1alpha1.ImageSpec{Name: "ubuntu-24.04"}
		}, false},
		{"image id alone", func(h *v1alpha1.HetznerProviderSpec) {
			h.ImageSelector = nil
			h.Image = &v1alpha1.ImageSpec{ID: 161547269}
		}, false},
		{"image selector alone", func(h *v1alpha1.HetznerProviderSpec) {
			h.ImageSelector = nil
			h.Image = &v1alpha1.ImageSpec{Selector: map[string]string{"k": "v"}}
		}, false},
		{"both blocks", func(h *v1alpha1.HetznerProviderSpec) {
			h.Image = &v1alpha1.ImageSpec{Name: "ubuntu-24.04"}
		}, true},
		{"neither block", func(h *v1alpha1.HetznerProviderSpec) {
			h.ImageSelector = nil
		}, true},
		{"two image variants", func(h *v1alpha1.HetznerProviderSpec) {
			h.ImageSelector = nil
			h.Image = &v1alpha1.ImageSpec{Name: "ubuntu-24.04", ID: 1}
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			config := validProviderConfig(objectName(t))
			tc.mutate(config.Spec.Hetzner)
			assertCreate(t, c, config, tc.wantRejected)
		})
	}
}

func TestProviderConfigStatusCapsThePublishedCatalogue(t *testing.T) {
	tests := []struct {
		name         string
		count        int
		wantRejected bool
	}{
		{"at the cap", v1alpha1.MaxPublishedInstanceTypes, false},
		{"one over the cap", v1alpha1.MaxPublishedInstanceTypes + 1, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			config := validProviderConfig(objectName(t))
			assertCreate(t, c, config, false)

			config.Status.InstanceTypes = publishedCatalogue(tc.count)
			err := c.Status().Update(t.Context(), config)
			switch {
			case tc.wantRejected && err == nil:
				t.Fatalf("apiserver accepted %d published instance types, want rejection", tc.count)
			case !tc.wantRejected && err != nil:
				t.Fatalf("apiserver rejected %d published instance types: %v", tc.count, err)
			}
		})
	}
}

func publishedCatalogue(count int) []v1alpha1.InstanceType {
	types := make([]v1alpha1.InstanceType, 0, count)
	for i := range count {
		types = append(types, v1alpha1.InstanceType{
			Name:         fmt.Sprintf("cx%d", i),
			Region:       "nbg1",
			Architecture: "x86",
			CPUType:      "shared",
			CPUCores:     2,
			MemoryBytes:  4 << 30,
			DiskBytes:    40 << 30,
			HourlyRate:   "0.0074",
			Currency:     "EUR",
			Available:    true,
		})
	}
	return types
}

func rawWatchdogProviderConfig(t *testing.T, field, value string) *unstructured.Unstructured {
	t.Helper()
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(validProviderConfig(objectName(t)))
	if err != nil {
		t.Fatalf("convert provider config: %v", err)
	}
	config := &unstructured.Unstructured{Object: content}
	config.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ProviderConfig"))
	if err := unstructured.SetNestedField(config.Object, value, "spec", "watchdog", field); err != nil {
		t.Fatalf("set spec.watchdog.%s: %v", field, err)
	}
	return config
}
