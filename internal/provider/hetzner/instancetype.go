package hetzner

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/lucawalz/horizon/internal/provider"
)

const bytesPerGiB int64 = 1 << 30

func (c *Client) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	if region == "" {
		return nil, fmt.Errorf("hetzner: instance type region is required")
	}
	serverTypes, err := c.serverTypes.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("hetzner: list server types: %w", err)
	}
	out := make([]provider.InstanceType, 0, len(serverTypes))
	for _, st := range serverTypes {
		row, offered, err := instanceTypeForRegion(st, region)
		if err != nil {
			return nil, err
		}
		if !offered {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func instanceTypeForRegion(st *hcloudgo.ServerType, region string) (provider.InstanceType, bool, error) {
	loc, ok := serverTypeLocation(st, region)
	if !ok {
		return provider.InstanceType{}, false, nil
	}
	pricing, ok := serverTypePricing(st, region)
	if !ok {
		return provider.InstanceType{}, false, fmt.Errorf("hetzner: server type %q has no pricing for region %q", st.Name, region)
	}
	rate, err := netHourlyRate(pricing.Hourly)
	if err != nil {
		return provider.InstanceType{}, false, fmt.Errorf("hetzner: server type %q: %w", st.Name, err)
	}
	return provider.InstanceType{
		Name:         st.Name,
		Architecture: string(st.Architecture),
		CPUType:      string(st.CPUType),
		CPUCores:     st.Cores,
		MemoryBytes:  int64(math.Round(float64(st.Memory) * float64(bytesPerGiB))),
		DiskBytes:    int64(st.Disk) * bytesPerGiB,
		Region:       region,
		Available:    loc.Available,
		Deprecated:   loc.IsDeprecated(),
		HourlyRate:   rate,
	}, true, nil
}

func serverTypeLocation(st *hcloudgo.ServerType, region string) (hcloudgo.ServerTypeLocation, bool) {
	for _, loc := range st.Locations {
		if loc.Location != nil && loc.Location.Name == region {
			return loc, true
		}
	}
	return hcloudgo.ServerTypeLocation{}, false
}

func serverTypePricing(st *hcloudgo.ServerType, region string) (hcloudgo.ServerTypeLocationPricing, bool) {
	for _, p := range st.Pricings {
		if p.Location != nil && p.Location.Name == region {
			return p, true
		}
	}
	return hcloudgo.ServerTypeLocationPricing{}, false
}

func netHourlyRate(price hcloudgo.Price) (provider.Rate, error) {
	amount, err := strconv.ParseFloat(price.Net, 64)
	if err != nil {
		return provider.Rate{}, fmt.Errorf("parse net hourly price %q: %w", price.Net, err)
	}
	return provider.Rate{Amount: amount, Currency: price.Currency}, nil
}
