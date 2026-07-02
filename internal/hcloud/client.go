package hcloud

import (
	"context"
	"fmt"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
)

const (
	PoolLabelKey       = "horizon.dev/pool"
	ReservedPoolValue  = "reserved"
	ManagedByLabelKey  = "horizon.dev/managed-by"
	ManagedByValue     = "horizon"
	NodeGroupLabelKey  = "hcloud/node-group"
	ImageSelectorLabel = "caph-image-name"
)

type ServerAPI interface {
	AllWithOpts(ctx context.Context, opts hcloudgo.ServerListOpts) ([]*hcloudgo.Server, error)
	Create(ctx context.Context, opts hcloudgo.ServerCreateOpts) (hcloudgo.ServerCreateResult, *hcloudgo.Response, error)
	Delete(ctx context.Context, server *hcloudgo.Server) (*hcloudgo.Response, error)
}

type ImageAPI interface {
	AllWithOpts(ctx context.Context, opts hcloudgo.ImageListOpts) ([]*hcloudgo.Image, error)
}

type SSHKeyAPI interface {
	GetByName(ctx context.Context, name string) (*hcloudgo.SSHKey, *hcloudgo.Response, error)
}

type Client struct {
	servers ServerAPI
	images  ImageAPI
	sshKeys SSHKeyAPI
}

func NewClient(token string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("hcloud: token must not be empty")
	}
	cl := hcloudgo.NewClient(hcloudgo.WithToken(token))
	return &Client{servers: &cl.Server, images: &cl.Image, sshKeys: &cl.SSHKey}, nil
}

func NewClientWithAPIs(servers ServerAPI, images ImageAPI, sshKeys SSHKeyAPI) *Client {
	return &Client{servers: servers, images: images, sshKeys: sshKeys}
}
