package controller

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

const (
	podNamespaceEnvVar   = "POD_NAMESPACE"
	serviceAccountNSPath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

func NewProviderFactory(kc kubernetes.Interface) ProviderFactory {
	namespace, namespaceErr := OperatorNamespace()
	return func(ctx context.Context, cfg *v1alpha1.ProviderConfig) (provider.Provider, error) {
		if namespaceErr != nil {
			return nil, namespaceErr
		}
		switch cfg.Spec.Type {
		case v1alpha1.ProviderTypeHetzner:
			return hetznerProvider(ctx, kc, namespace, cfg.Spec.Hetzner)
		default:
			return nil, fmt.Errorf("unsupported provider type %q", cfg.Spec.Type)
		}
	}
}

func OperatorNamespace() (string, error) {
	if namespace := os.Getenv(podNamespaceEnvVar); namespace != "" {
		return namespace, nil
	}
	data, err := os.ReadFile(serviceAccountNSPath)
	if err != nil {
		return "", fmt.Errorf("resolve operator namespace, set %s: %w", podNamespaceEnvVar, err)
	}
	namespace := strings.TrimSpace(string(data))
	if namespace == "" {
		return "", fmt.Errorf("resolve operator namespace, %s is empty", serviceAccountNSPath)
	}
	return namespace, nil
}

func hetznerProvider(ctx context.Context, kc kubernetes.Interface, namespace string, spec *v1alpha1.HetznerProviderSpec) (provider.Provider, error) {
	if spec == nil {
		return nil, fmt.Errorf("provider type %q carries no hetzner block", v1alpha1.ProviderTypeHetzner)
	}
	token, err := secretValue(ctx, kc, namespace, spec.CredentialsSecretRef)
	if err != nil {
		return nil, err
	}
	userData, err := secretValue(ctx, kc, namespace, spec.CloudInitSecretRef)
	if err != nil {
		return nil, err
	}
	label, value, err := imageSelector(spec.ImageSelector)
	if err != nil {
		return nil, err
	}
	return hetzner.NewClient(token, hetzner.ServerSpec{
		ImageLabel: label,
		ImageValue: value,
		SSHKeys:    slices.Clone(spec.SSHKeys),
		UserData:   userData,
	})
}

func imageSelector(selector map[string]string) (label, value string, err error) {
	keys := slices.Sorted(maps.Keys(selector))
	if len(keys) != 1 {
		return "", "", fmt.Errorf("imageSelector must carry exactly one label, got %d", len(keys))
	}
	return keys[0], selector[keys[0]], nil
}

func secretValue(ctx context.Context, kc kubernetes.Interface, namespace string, ref corev1.SecretKeySelector) (string, error) {
	if ref.Name == "" || ref.Key == "" {
		return "", fmt.Errorf("secret reference needs both a name and a key")
	}
	secret, err := kc.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read secret %s/%s: %w", namespace, ref.Name, err)
	}
	data, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	return string(data), nil
}
