package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	reasonProviderConfigReady   = "Ready"
	reasonSecretUnresolved      = "SecretUnresolved"
	reasonTeardownNotGuaranteed = "TeardownNotGuaranteed"
	reasonCatalogueUnavailable  = "CatalogueUnavailable"
	reasonProviderUnsupported   = "ProviderUnsupported"
	reasonCataloguePublished    = "Published"
	reasonCatalogueTruncated    = "Truncated"
	reasonCatalogueEmpty        = "Empty"
)

const readyMessage = "every referenced secret resolves, teardown is guaranteed and the provider answered the instance type query"

type ProviderConfigPublisher struct {
	client    client.Client
	kube      kubernetes.Interface
	namespace string
}

func NewProviderConfigPublisher(api client.Client, kc kubernetes.Interface) (*ProviderConfigPublisher, error) {
	namespace, err := operatorNamespace()
	if err != nil {
		return nil, err
	}
	return &ProviderConfigPublisher{client: api, kube: kc, namespace: namespace}, nil
}

func (p *ProviderConfigPublisher) Publish(
	ctx context.Context, cfg *v1alpha1.ProviderConfig, types []provider.InstanceType, fetchErr error,
) error {
	desired := p.resolve(ctx, cfg, types, fetchErr)
	key := client.ObjectKeyFromObject(cfg)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var live v1alpha1.ProviderConfig
		if err := p.client.Get(ctx, key, &live); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("read provider config %q: %w", key.Name, err)
		}
		if !desired.applyTo(&live) {
			return nil
		}
		return p.client.Status().Update(ctx, &live)
	})
}

type providerConfigStatus struct {
	ready     metav1.Condition
	published metav1.Condition
	types     []v1alpha1.InstanceType
	fetched   bool
}

func (s providerConfigStatus) applyTo(live *v1alpha1.ProviderConfig) bool {
	// two replicas racing on the same probe result write the same content, so the second observes no change and skips
	s.ready.ObservedGeneration = live.Generation
	s.published.ObservedGeneration = live.Generation

	readyChanged := meta.SetStatusCondition(&live.Status.Conditions, s.ready)
	publishedChanged := meta.SetStatusCondition(&live.Status.Conditions, s.published)

	// a failed fetch keeps the last published catalogue rather than wiping a good one over a transient outage
	typesChanged := s.fetched && !slices.Equal(live.Status.InstanceTypes, s.types)
	if typesChanged {
		live.Status.InstanceTypes = s.types
	}
	return readyChanged || publishedChanged || typesChanged
}

func (p *ProviderConfigPublisher) resolve(
	ctx context.Context, cfg *v1alpha1.ProviderConfig, types []provider.InstanceType, fetchErr error,
) providerConfigStatus {
	published, truncated := publishableCatalogue(types)
	return providerConfigStatus{
		ready:     p.readiness(ctx, cfg, fetchErr),
		published: catalogueCondition(fetchErr, len(types), truncated),
		types:     published,
		fetched:   fetchErr == nil && len(types) > 0,
	}
}

func (p *ProviderConfigPublisher) readiness(ctx context.Context, cfg *v1alpha1.ProviderConfig, fetchErr error) metav1.Condition {
	profile, err := profileOf(cfg)
	if err != nil {
		return unreadyCondition(reasonProviderUnsupported, err)
	}
	if err := p.resolveSecretRefs(ctx, profile.secretRefs); err != nil {
		return unreadyCondition(reasonSecretUnresolved, err)
	}
	if err := requireTeardownGuarantee(cfg, profile.capabilities); err != nil {
		return unreadyCondition(reasonTeardownNotGuaranteed, err)
	}
	if fetchErr != nil {
		return unreadyCondition(reasonCatalogueUnavailable, fetchErr)
	}
	return metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  reasonProviderConfigReady,
		Message: readyMessage,
	}
}

func (p *ProviderConfigPublisher) resolveSecretRefs(ctx context.Context, refs []namedSecretRef) error {
	for _, named := range refs {
		if _, err := secretValue(ctx, p.kube, p.namespace, named.field, *named.ref); err != nil {
			return err
		}
	}
	return nil
}

func unreadyCondition(reason string, cause error) metav1.Condition {
	return metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: cause.Error(),
	}
}

func catalogueCondition(fetchErr error, offered int, truncated bool) metav1.Condition {
	condition := metav1.Condition{Type: v1alpha1.ConditionCataloguePublished, Status: metav1.ConditionFalse}
	switch {
	case fetchErr != nil:
		condition.Reason = reasonCatalogueUnavailable
		condition.Message = fetchErr.Error()
	case offered == 0:
		condition.Reason = reasonCatalogueEmpty
		condition.Message = "the provider answered with no instance type in any region"
	case truncated:
		condition.Reason = reasonCatalogueTruncated
		condition.Message = fmt.Sprintf("the provider offers %d instance types, more than the %d status holds, so the published catalogue is truncated",
			offered, v1alpha1.MaxPublishedInstanceTypes)
	default:
		condition.Status = metav1.ConditionTrue
		condition.Reason = reasonCataloguePublished
		condition.Message = fmt.Sprintf("the provider offers %d instance types", offered)
	}
	return condition
}

func publishableCatalogue(types []provider.InstanceType) ([]v1alpha1.InstanceType, bool) {
	published := make([]v1alpha1.InstanceType, 0, len(types))
	for _, offered := range types {
		published = append(published, publishedInstanceType(offered))
	}
	slices.SortFunc(published, func(a, b v1alpha1.InstanceType) int {
		if byRegion := strings.Compare(a.Region, b.Region); byRegion != 0 {
			return byRegion
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(published) <= v1alpha1.MaxPublishedInstanceTypes {
		return published, false
	}
	return published[:v1alpha1.MaxPublishedInstanceTypes], true
}

func publishedInstanceType(offered provider.InstanceType) v1alpha1.InstanceType {
	return v1alpha1.InstanceType{
		Name:         offered.Name,
		Region:       offered.Region,
		Architecture: offered.Architecture,
		CPUType:      offered.CPUType,
		CPUCores:     int32(offered.CPUCores),
		MemoryBytes:  offered.MemoryBytes,
		DiskBytes:    offered.DiskBytes,
		HourlyRate:   formatHourlyRate(offered.HourlyRate),
		Currency:     offered.HourlyRate.Currency,
		Available:    offered.Available,
		Deprecated:   offered.Deprecated,
	}
}
