// Package metered times every provider call and reports it as a provider request metric.
package metered

import (
	"context"
	"errors"
	"time"

	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

// the inner provider is held in a field rather than embedded, so a new interface method cannot pass through untimed
type Provider struct {
	inner  provider.Provider
	config string
	now    func() time.Time
}

var _ provider.Provider = (*Provider)(nil)

func Wrap(providerConfig string, inner provider.Provider) *Provider {
	return &Provider{inner: inner, config: providerConfig, now: time.Now}
}

func (p *Provider) Capabilities() provider.Capabilities {
	return p.inner.Capabilities()
}

func (p *Provider) Create(ctx context.Context, req provider.CreateRequest) (provider.Instance, error) {
	return observe(p, metrics.OperationCreate, func() (provider.Instance, error) {
		return p.inner.Create(ctx, req)
	})
}

func (p *Provider) Get(ctx context.Context, name string) (provider.Instance, error) {
	return observe(p, metrics.OperationGet, func() (provider.Instance, error) {
		return p.inner.Get(ctx, name)
	})
}

func (p *Provider) List(ctx context.Context, selector map[string]string) ([]provider.Instance, error) {
	return observe(p, metrics.OperationList, func() ([]provider.Instance, error) {
		return p.inner.List(ctx, selector)
	})
}

func (p *Provider) Delete(ctx context.Context, name string) error {
	_, err := observe(p, metrics.OperationDelete, func() (struct{}, error) {
		return struct{}{}, p.inner.Delete(ctx, name)
	})
	return err
}

func (p *Provider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return observe(p, metrics.OperationListInstanceTypes, func() ([]provider.InstanceType, error) {
		return p.inner.ListInstanceTypes(ctx, region)
	})
}

func observe[T any](p *Provider, operation metrics.Operation, call func() (T, error)) (T, error) {
	started := p.now()
	out, err := call()
	metrics.ObserveProviderRequest(p.config, operation, classify(err), p.now().Sub(started))
	return out, err
}

func classify(err error) metrics.Result {
	switch {
	case err == nil:
		return metrics.ResultSuccess
	case errors.Is(err, provider.ErrNotFound):
		return metrics.ResultNotFound
	case errors.Is(err, context.Canceled):
		return metrics.ResultCanceled
	default:
		return metrics.ResultFailure
	}
}
