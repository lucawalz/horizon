package capi

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/internal/k8s"
)

type Client struct {
	cl client.Client
}

func NewClient(kubeconfigPath string) (*Client, error) {
	restCfg, err := k8s.RestConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("capi: rest config: %w", err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}
	restCfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	scheme, err := NewScheme()
	if err != nil {
		return nil, err
	}
	cl, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("capi: client: %w", err)
	}
	return &Client{cl: cl}, nil
}

func NewClientWithCRClient(cl client.Client) *Client {
	return &Client{cl: cl}
}

func (c *Client) crClient() client.Client {
	return c.cl
}
