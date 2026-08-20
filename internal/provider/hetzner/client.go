package hetzner

import (
	"context"
	"fmt"
	"slices"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	NodeGroupLabelKey = "hcloud/node-group"
	ProviderIDPrefix  = "hcloud://"
)

var locations = []string{"fsn1", "nbg1", "hel1", "ash", "hil", "sin"}

type ServerAPI interface {
	AllWithOpts(ctx context.Context, opts hcloudgo.ServerListOpts) ([]*hcloudgo.Server, error)
	Create(ctx context.Context, opts hcloudgo.ServerCreateOpts) (hcloudgo.ServerCreateResult, *hcloudgo.Response, error)
	Delete(ctx context.Context, server *hcloudgo.Server) (*hcloudgo.Response, error)
}

type ImageAPI interface {
	AllWithOpts(ctx context.Context, opts hcloudgo.ImageListOpts) ([]*hcloudgo.Image, error)
}

type ServerTypeAPI interface {
	GetByName(ctx context.Context, name string) (*hcloudgo.ServerType, *hcloudgo.Response, error)
	All(ctx context.Context) ([]*hcloudgo.ServerType, error)
}

type SSHKeyAPI interface {
	GetByName(ctx context.Context, name string) (*hcloudgo.SSHKey, *hcloudgo.Response, error)
}

type FirewallAPI interface {
	GetByName(ctx context.Context, name string) (*hcloudgo.Firewall, *hcloudgo.Response, error)
}

type Client struct {
	servers     ServerAPI
	images      ImageAPI
	sshKeys     SSHKeyAPI
	firewalls   FirewallAPI
	serverTypes ServerTypeAPI
	spec        ServerSpec
}

var _ provider.Provider = (*Client)(nil)

func NewClient(token string, spec ServerSpec) (provider.Provider, error) {
	client, err := newClient(token, spec)
	if err != nil {
		return nil, err
	}
	if _, err := buildUserData(spec.UserData); err != nil {
		return nil, err
	}
	return client, nil
}

func NewTokenClient(token string) (provider.Provider, error) {
	client, err := newClient(token, ServerSpec{})
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newClient(token string, spec ServerSpec) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("hetzner: token must not be empty")
	}
	cl := hcloudgo.NewClient(hcloudgo.WithToken(token))
	return &Client{servers: &cl.Server, images: &cl.Image, sshKeys: &cl.SSHKey, firewalls: &cl.Firewall, serverTypes: &cl.ServerType, spec: spec}, nil
}

func NewClientWithAPIs(servers ServerAPI, images ImageAPI, sshKeys SSHKeyAPI, firewalls FirewallAPI, serverTypes ServerTypeAPI, spec ServerSpec) *Client {
	return &Client{servers: servers, images: images, sshKeys: sshKeys, firewalls: firewalls, serverTypes: serverTypes, spec: spec}
}

func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		SelfTerminationStopsBilling: false,
		SupportsResourceLabels:      true,
		Regions:                     slices.Clone(locations),
	}
}
