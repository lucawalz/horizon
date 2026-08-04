package hetzner

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type ImageRef struct {
	Name     string
	ID       int64
	Selector map[string]string
}

func (r ImageRef) empty() bool {
	return r.ID == 0 && r.Name == "" && len(r.Selector) == 0
}

func (c *Client) resolveImage(ctx context.Context, ref ImageRef, serverType string) (*hcloudgo.Image, error) {
	if ref.empty() {
		return nil, fmt.Errorf("hetzner: no image is configured")
	}
	if ref.ID != 0 {
		return &hcloudgo.Image{ID: ref.ID}, nil
	}
	arch, err := c.architectureFor(ctx, serverType)
	if err != nil {
		return nil, err
	}
	if ref.Name != "" {
		return c.imageByName(ctx, ref.Name, arch)
	}
	return c.imageBySelector(ctx, ref.Selector, arch)
}

func (c *Client) architectureFor(ctx context.Context, serverType string) (hcloudgo.Architecture, error) {
	found, _, err := c.serverTypes.GetByName(ctx, serverType)
	if err != nil {
		return "", fmt.Errorf("hetzner: lookup server type %q: %w", serverType, err)
	}
	if found == nil {
		return "", fmt.Errorf("hetzner: server type %q not found", serverType)
	}
	return found.Architecture, nil
}

func (c *Client) imageByName(ctx context.Context, name string, arch hcloudgo.Architecture) (*hcloudgo.Image, error) {
	images, err := c.images.AllWithOpts(ctx, hcloudgo.ImageListOpts{
		Name:         name,
		Architecture: []hcloudgo.Architecture{arch},
	})
	if err != nil {
		return nil, fmt.Errorf("hetzner: list images named %q: %w", name, err)
	}
	switch len(images) {
	case 0:
		return nil, fmt.Errorf("hetzner: no image named %q for architecture %s", name, arch)
	case 1:
		return images[0], nil
	default:
		return nil, fmt.Errorf("hetzner: image name %q matches %d images for architecture %s", name, len(images), arch)
	}
}

func (c *Client) imageBySelector(ctx context.Context, selector map[string]string, arch hcloudgo.Architecture) (*hcloudgo.Image, error) {
	expr := labelSelectorExpr(selector)
	images, err := c.images.AllWithOpts(ctx, hcloudgo.ImageListOpts{
		ListOpts:     hcloudgo.ListOpts{LabelSelector: expr},
		Architecture: []hcloudgo.Architecture{arch},
	})
	if err != nil {
		return nil, fmt.Errorf("hetzner: list images %q: %w", expr, err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("hetzner: no image matches label %q for architecture %s", expr, arch)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Created.After(images[j].Created) })
	return images[0], nil
}

func labelSelectorExpr(selector map[string]string) string {
	parts := make([]string, 0, len(selector))
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		parts = append(parts, key+"="+selector[key])
	}
	return strings.Join(parts, ",")
}
