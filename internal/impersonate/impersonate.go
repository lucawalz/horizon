// Package impersonate builds a cluster client per request that reaches the apiserver as the verified end user rather than as this process.
package impersonate

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/lucawalz/horizon/internal/web"
)

type Clients struct {
	base   *rest.Config
	scheme *runtime.Scheme
	mapper meta.RESTMapper
}

var (
	_ web.ReaderFactory = (*Clients)(nil)
	_ web.WriterFactory = (*Clients)(nil)
)

// the resource mapping is the same for every caller, so it is discovered once rather than on every request
func New(base *rest.Config, scheme *runtime.Scheme) (*Clients, error) {
	if base == nil {
		return nil, errors.New("impersonate: a base cluster config is required")
	}
	if scheme == nil {
		return nil, errors.New("impersonate: a scheme is required")
	}

	discovery, err := rest.HTTPClientFor(base)
	if err != nil {
		return nil, fmt.Errorf("build a discovery client: %w", err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(base, discovery)
	if err != nil {
		return nil, fmt.Errorf("map the resources of the cluster: %w", err)
	}
	return &Clients{base: base, scheme: scheme, mapper: mapper}, nil
}

func (c *Clients) ReaderFor(identity web.Identity) (client.Reader, error) {
	return c.clientFor(identity)
}

func (c *Clients) WriterFor(identity web.Identity) (web.LeaseWriter, error) {
	api, err := c.clientFor(identity)
	if err != nil {
		return nil, err
	}
	return web.LeaseWriterFor(api), nil
}

// an unnamed identity sends no impersonation header at all, so the request would reach the cluster as this process
func (c *Clients) clientFor(identity web.Identity) (client.Client, error) {
	if identity.Username == "" {
		return nil, errors.New("impersonate: an identity with no name cannot be impersonated")
	}
	api, err := client.New(as(c.base, identity), client.Options{Scheme: c.scheme, Mapper: c.mapper})
	if err != nil {
		return nil, fmt.Errorf("build a cluster client for %s: %w", identity.Username, err)
	}
	return api, nil
}

// the base config is shared by every request in flight, so it is copied rather than impersonated in place
func as(base *rest.Config, identity web.Identity) *rest.Config {
	config := rest.CopyConfig(base)
	config.Impersonate = rest.ImpersonationConfig{UserName: identity.Username, Groups: identity.Groups}
	return config
}
