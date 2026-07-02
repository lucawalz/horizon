package hcloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type ServerSpec struct {
	Location   string
	ServerType string
	ImageLabel string
	ImageValue string
	SSHKeys    []string
	UserData   string
}

type Server struct {
	ID     int64
	Name   string
	Labels map[string]string
}

func reservedLabels() map[string]string {
	return map[string]string{
		PoolLabelKey:      ReservedPoolValue,
		ManagedByLabelKey: ManagedByValue,
	}
}

func ownedByHorizon(labels map[string]string) bool {
	if labels[ManagedByLabelKey] != ManagedByValue {
		return false
	}
	if _, ok := labels[NodeGroupLabelKey]; ok {
		return false
	}
	return true
}

func (c *Client) resolveImage(ctx context.Context, label, value string) (*hcloudgo.Image, error) {
	if label == "" {
		label = ImageSelectorLabel
	}
	selector := label + "=" + value
	images, err := c.images.AllWithOpts(ctx, hcloudgo.ImageListOpts{
		ListOpts: hcloudgo.ListOpts{LabelSelector: selector},
	})
	if err != nil {
		return nil, fmt.Errorf("hcloud: list images %q: %w", selector, err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("hcloud: no image matches label %q", selector)
	}
	sort.Slice(images, func(i, j int) bool {
		return images[i].Created.After(images[j].Created)
	})
	return images[0], nil
}

func reservedServerName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("hcloud: generate server name: %w", err)
	}
	return ReservedPoolValue + "-" + hex.EncodeToString(b), nil
}

func (c *Client) CreateReservedServer(ctx context.Context, spec ServerSpec) (*Server, error) {
	if spec.Location == "" || spec.ServerType == "" {
		return nil, fmt.Errorf("hcloud: server location and type are required")
	}
	if spec.ImageValue == "" {
		return nil, fmt.Errorf("hcloud: reserved.image.value is required")
	}
	if spec.UserData == "" {
		return nil, fmt.Errorf("hcloud: server user-data is required")
	}
	image, err := c.resolveImage(ctx, spec.ImageLabel, spec.ImageValue)
	if err != nil {
		return nil, err
	}
	name, err := reservedServerName()
	if err != nil {
		return nil, err
	}
	sshKeys := make([]*hcloudgo.SSHKey, 0, len(spec.SSHKeys))
	for _, name := range spec.SSHKeys {
		key, _, err := c.sshKeys.GetByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("hcloud: lookup ssh key %q: %w", name, err)
		}
		if key == nil {
			return nil, fmt.Errorf("hcloud: ssh key %q not found in project", name)
		}
		sshKeys = append(sshKeys, key)
	}
	opts := hcloudgo.ServerCreateOpts{
		Name:       name,
		ServerType: &hcloudgo.ServerType{Name: spec.ServerType},
		Image:      image,
		Location:   &hcloudgo.Location{Name: spec.Location},
		SSHKeys:    sshKeys,
		UserData:   spec.UserData,
		Labels:     reservedLabels(),
	}
	res, _, err := c.servers.Create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("hcloud: create reserved server: %w", err)
	}
	return &Server{ID: res.Server.ID, Name: res.Server.Name, Labels: res.Server.Labels}, nil
}

func (c *Client) ListReservedServers(ctx context.Context) ([]Server, error) {
	selector := ManagedByLabelKey + "=" + ManagedByValue
	raw, err := c.servers.AllWithOpts(ctx, hcloudgo.ServerListOpts{
		ListOpts: hcloudgo.ListOpts{LabelSelector: selector},
	})
	if err != nil {
		return nil, fmt.Errorf("hcloud: list reserved servers: %w", err)
	}
	out := make([]Server, 0, len(raw))
	for _, s := range raw {
		if !ownedByHorizon(s.Labels) {
			continue
		}
		out = append(out, Server{ID: s.ID, Name: s.Name, Labels: s.Labels})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) deleteReservedServer(ctx context.Context, s Server) error {
	if !ownedByHorizon(s.Labels) {
		return fmt.Errorf("hcloud: refusing to delete server %q (%d): not labelled %s=%s",
			s.Name, s.ID, ManagedByLabelKey, ManagedByValue)
	}
	if _, err := c.servers.Delete(ctx, &hcloudgo.Server{ID: s.ID}); err != nil {
		return fmt.Errorf("hcloud: delete server %q: %w", s.Name, err)
	}
	return nil
}

func (c *Client) ScaleReservedTo(ctx context.Context, spec ServerSpec, want int) (int, error) {
	if want < 0 {
		return 0, fmt.Errorf("hcloud: desired reserved count must not be negative")
	}
	current, err := c.ListReservedServers(ctx)
	if err != nil {
		return 0, err
	}
	have := len(current)
	switch {
	case have < want:
		for i := have; i < want; i++ {
			if _, err := c.CreateReservedServer(ctx, spec); err != nil {
				return i, err
			}
		}
	case have > want:
		for i := have - 1; i >= want; i-- {
			if err := c.deleteReservedServer(ctx, current[i]); err != nil {
				return 0, err
			}
		}
	}
	return want, nil
}
