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
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
	"github.com/lucawalz/horizon/internal/version"
)

const podNamespaceEnvVar = "POD_NAMESPACE"

var serviceAccountNSPath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

func NewProviderFactory(kc kubernetes.Interface) (ProviderFactory, error) {
	namespace, err := operatorNamespace()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, cfg *v1alpha1.ProviderConfig) (provider.Provider, error) {
		switch cfg.Spec.Type {
		case v1alpha1.ProviderTypeHetzner:
			return hetznerProvider(ctx, kc, namespace, cfg.Spec.Hetzner, cfg.Spec.Watchdog)
		default:
			return nil, fmt.Errorf("unsupported provider type %q", cfg.Spec.Type)
		}
	}, nil
}

func NewCatalogueFactory(kc kubernetes.Interface) (catalogue.ListerFactory, error) {
	namespace, err := operatorNamespace()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, cfg *v1alpha1.ProviderConfig) (catalogue.Lister, error) {
		switch cfg.Spec.Type {
		case v1alpha1.ProviderTypeHetzner:
			return hetznerCatalogue(ctx, kc, namespace, cfg.Spec.Hetzner)
		default:
			return nil, fmt.Errorf("unsupported provider type %q", cfg.Spec.Type)
		}
	}, nil
}

func hetznerCatalogue(ctx context.Context, kc kubernetes.Interface, namespace string, spec *v1alpha1.HetznerProviderSpec) (catalogue.Lister, error) {
	if spec == nil {
		return nil, fmt.Errorf("provider type %q carries no hetzner block", v1alpha1.ProviderTypeHetzner)
	}
	token, err := secretValue(ctx, kc, namespace, "credentialsSecretRef", spec.CredentialsSecretRef)
	if err != nil {
		return nil, err
	}
	client, err := hetzner.NewTokenClient(token)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func nodeCredentialConfigured(spec v1alpha1.ProviderConfigSpec) bool {
	return spec.Hetzner != nil && spec.Hetzner.NodeCredentialSecretRef != nil
}

func requireTeardownGuarantee(cfg *v1alpha1.ProviderConfig, prov provider.Provider) error {
	if prov.Capabilities().SelfTerminationStopsBilling || nodeCredentialConfigured(cfg.Spec) {
		return nil
	}
	return fmt.Errorf("providerconfig %q cannot stop billing by self-terminating and configures no nodeCredentialSecretRef, so teardown of new capacity is not guaranteed", cfg.Name)
}

func operatorNamespace() (string, error) {
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

func hetznerProvider(ctx context.Context, kc kubernetes.Interface, namespace string, spec *v1alpha1.HetznerProviderSpec, watchdog v1alpha1.WatchdogPolicy) (provider.Provider, error) {
	if spec == nil {
		return nil, fmt.Errorf("provider type %q carries no hetzner block", v1alpha1.ProviderTypeHetzner)
	}
	token, err := secretValue(ctx, kc, namespace, "credentialsSecretRef", spec.CredentialsSecretRef)
	if err != nil {
		return nil, err
	}
	template, err := secretValue(ctx, kc, namespace, "cloudInitSecretRef", spec.CloudInitSecretRef)
	if err != nil {
		return nil, err
	}
	userData, err := renderCloudInit(ctx, kc, namespace, spec, watchdog, template)
	if err != nil {
		return nil, err
	}
	ref, err := imageRef(spec)
	if err != nil {
		return nil, err
	}
	return hetzner.NewClient(token, hetzner.ServerSpec{
		Image:     ref,
		SSHKeys:   slices.Clone(spec.SSHKeys),
		Firewalls: slices.Clone(spec.Firewalls),
		UserData:  userData,
	})
}

type sentinelSource struct {
	sentinel string
	field    string
	ref      *corev1.SecretKeySelector
}

func secretBackedSentinels(spec *v1alpha1.HetznerProviderSpec) []sentinelSource {
	return []sentinelSource{
		{hetzner.NodeTokenSentinel, "nodeCredentialSecretRef", spec.NodeCredentialSecretRef},
		{hetzner.JoinTokenSentinel, "joinTokenSecretRef", spec.JoinTokenSecretRef},
	}
}

func renderCloudInit(ctx context.Context, kc kubernetes.Interface, namespace string, spec *v1alpha1.HetznerProviderSpec, watchdog v1alpha1.WatchdogPolicy, template string) (string, error) {
	values := map[string]string{
		hetzner.VersionSentinel:     version.Version(),
		hetzner.MaxLifetimeSentinel: watchdog.MaxLifetime.Duration.String(),
	}
	for _, source := range secretBackedSentinels(spec) {
		if source.ref == nil {
			if strings.Contains(template, source.sentinel) {
				return "", fmt.Errorf("cloud-init needs %s but spec.hetzner sets no %s", source.sentinel, source.field)
			}
			continue
		}
		value, err := secretValue(ctx, kc, namespace, source.field, *source.ref)
		if err != nil {
			return "", err
		}
		values[source.sentinel] = value
	}
	return hetzner.RenderUserData(template, values)
}

func imageRef(spec *v1alpha1.HetznerProviderSpec) (hetzner.ImageRef, error) {
	if spec.Image != nil && len(spec.ImageSelector) > 0 {
		return hetzner.ImageRef{}, fmt.Errorf("spec.hetzner sets both image and the deprecated imageSelector")
	}
	if spec.Image != nil {
		return hetzner.ImageRef{
			Name:     spec.Image.Name,
			ID:       spec.Image.ID,
			Selector: maps.Clone(spec.Image.Selector),
		}, nil
	}
	if len(spec.ImageSelector) > 0 {
		return hetzner.ImageRef{Selector: maps.Clone(spec.ImageSelector)}, nil
	}
	return hetzner.ImageRef{}, fmt.Errorf("spec.hetzner needs either image or the deprecated imageSelector")
}

func secretValue(ctx context.Context, kc kubernetes.Interface, namespace, field string, ref corev1.SecretKeySelector) (string, error) {
	if ref.Name == "" || ref.Key == "" {
		return "", fmt.Errorf("%s needs both a name and a key", field)
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
