package hetzner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/lucawalz/horizon/internal/provider"
)

type ServerSpec struct {
	Location   string
	ServerType string
	Image      ImageRef
	SSHKeys    []string
	Firewalls  []string
	UserData   string
}

func ownedByHorizon(labels map[string]string) bool {
	if labels[provider.ManagedByLabelKey] != provider.ManagedByValue {
		return false
	}
	if _, ok := labels[NodeGroupLabelKey]; ok {
		return false
	}
	return true
}

func managedSelector(selector map[string]string) string {
	parts := []string{provider.ManagedByLabelKey + "=" + provider.ManagedByValue}
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		if key == provider.ManagedByLabelKey {
			continue
		}
		parts = append(parts, key+"="+selector[key])
	}
	return strings.Join(parts, ",")
}

func instanceState(status hcloudgo.ServerStatus) provider.InstanceState {
	switch status {
	case hcloudgo.ServerStatusRunning:
		return provider.Running
	case hcloudgo.ServerStatusStopping, hcloudgo.ServerStatusOff, hcloudgo.ServerStatusDeleting:
		return provider.Terminating
	default:
		return provider.Provisioning
	}
}

func toInstance(s *hcloudgo.Server) provider.Instance {
	region := ""
	if s.Location != nil {
		region = s.Location.Name
	}
	return provider.Instance{
		Name:       s.Name,
		ProviderID: ProviderIDPrefix + strconv.FormatInt(s.ID, 10),
		Region:     region,
		State:      instanceState(s.Status),
		Labels:     maps.Clone(s.Labels),
		CreatedAt:  s.Created,
	}
}

func (c *Client) Create(ctx context.Context, req provider.CreateRequest) (provider.Instance, error) {
	if req.Name == "" {
		return provider.Instance{}, fmt.Errorf("hetzner: instance name is required")
	}
	if _, ok := req.Labels[NodeGroupLabelKey]; ok {
		return provider.Instance{}, fmt.Errorf("hetzner: refusing to create instance %q labelled %s: it could never be deleted", req.Name, NodeGroupLabelKey)
	}
	existing, err := c.findServer(ctx, req.Name)
	switch {
	case err == nil:
		return toInstance(existing), nil
	case !errors.Is(err, provider.ErrNotFound):
		return provider.Instance{}, err
	}
	opts, err := c.createOpts(ctx, req)
	if err != nil {
		return provider.Instance{}, err
	}
	res, _, err := c.servers.Create(ctx, opts)
	if err != nil {
		return provider.Instance{}, fmt.Errorf("hetzner: create instance %q: %w", req.Name, err)
	}
	return toInstance(res.Server), nil
}

func (c *Client) Get(ctx context.Context, name string) (provider.Instance, error) {
	s, err := c.findServer(ctx, name)
	if err != nil {
		return provider.Instance{}, err
	}
	return toInstance(s), nil
}

func (c *Client) List(ctx context.Context, selector map[string]string) ([]provider.Instance, error) {
	labelSelector := managedSelector(selector)
	raw, err := c.servers.AllWithOpts(ctx, hcloudgo.ServerListOpts{
		ListOpts: hcloudgo.ListOpts{LabelSelector: labelSelector},
	})
	if err != nil {
		return nil, fmt.Errorf("hetzner: list instances %q: %w", labelSelector, err)
	}
	out := make([]provider.Instance, 0, len(raw))
	for _, s := range raw {
		if !ownedByHorizon(s.Labels) {
			continue
		}
		out = append(out, toInstance(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) Delete(ctx context.Context, name string) error {
	s, err := c.findServer(ctx, name)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownedByHorizon(s.Labels) {
		return fmt.Errorf("hetzner: refusing to delete server %q (%d): not labelled %s=%s",
			s.Name, s.ID, provider.ManagedByLabelKey, provider.ManagedByValue)
	}
	if _, err := c.servers.Delete(ctx, &hcloudgo.Server{ID: s.ID}); err != nil {
		return fmt.Errorf("hetzner: delete instance %q: %w", name, err)
	}
	return nil
}

func (c *Client) findServer(ctx context.Context, name string) (*hcloudgo.Server, error) {
	if name == "" {
		return nil, fmt.Errorf("hetzner: instance name is required")
	}
	raw, err := c.servers.AllWithOpts(ctx, hcloudgo.ServerListOpts{Name: name})
	if err != nil {
		return nil, fmt.Errorf("hetzner: get instance %q: %w", name, err)
	}
	for _, s := range raw {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, fmt.Errorf("hetzner: instance %q: %w", name, provider.ErrNotFound)
}

func (c *Client) createOpts(ctx context.Context, req provider.CreateRequest) (hcloudgo.ServerCreateOpts, error) {
	location := valueOrDefault(req.Region, c.spec.Location)
	serverType := valueOrDefault(req.Size, c.spec.ServerType)
	userData := valueOrDefault(req.UserData, c.spec.UserData)
	if location == "" || serverType == "" {
		return hcloudgo.ServerCreateOpts{}, fmt.Errorf("hetzner: server location and type are required")
	}
	if c.spec.Image.empty() {
		return hcloudgo.ServerCreateOpts{}, fmt.Errorf("hetzner: spec.hetzner.image is required")
	}
	if userData == "" {
		return hcloudgo.ServerCreateOpts{}, fmt.Errorf("hetzner: server user-data is required")
	}
	image, err := c.resolveImage(ctx, c.spec.Image, serverType)
	if err != nil {
		return hcloudgo.ServerCreateOpts{}, err
	}
	sshKeys, err := c.resolveSSHKeys(ctx)
	if err != nil {
		return hcloudgo.ServerCreateOpts{}, err
	}
	firewalls, err := c.resolveFirewalls(ctx)
	if err != nil {
		return hcloudgo.ServerCreateOpts{}, err
	}
	return hcloudgo.ServerCreateOpts{
		Name:       req.Name,
		ServerType: &hcloudgo.ServerType{Name: serverType},
		Image:      image,
		Location:   &hcloudgo.Location{Name: location},
		SSHKeys:    sshKeys,
		Firewalls:  firewalls,
		UserData:   userData,
		Labels:     managedLabels(req.Labels),
	}, nil
}

func (c *Client) resolveSSHKeys(ctx context.Context) ([]*hcloudgo.SSHKey, error) {
	keys := make([]*hcloudgo.SSHKey, 0, len(c.spec.SSHKeys))
	for _, name := range c.spec.SSHKeys {
		key, _, err := c.sshKeys.GetByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("hetzner: lookup ssh key %q: %w", name, err)
		}
		if key == nil {
			return nil, fmt.Errorf("hetzner: ssh key %q not found in project", name)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (c *Client) resolveFirewalls(ctx context.Context) ([]*hcloudgo.ServerCreateFirewall, error) {
	firewalls := make([]*hcloudgo.ServerCreateFirewall, 0, len(c.spec.Firewalls))
	for _, name := range c.spec.Firewalls {
		firewall, _, err := c.firewalls.GetByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("hetzner: lookup firewall %q: %w", name, err)
		}
		if firewall == nil {
			return nil, fmt.Errorf("hetzner: firewall %q not found in project", name)
		}
		firewalls = append(firewalls, &hcloudgo.ServerCreateFirewall{Firewall: *firewall})
	}
	return firewalls, nil
}

func managedLabels(requested map[string]string) map[string]string {
	labels := maps.Clone(requested)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[provider.ManagedByLabelKey] = provider.ManagedByValue
	return labels
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
