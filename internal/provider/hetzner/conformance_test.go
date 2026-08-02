package hetzner

import (
	"testing"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/conformance"
)

func TestHetznerSatisfiesTheProviderContract(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		c, f := newFake(provisionableSpec(), poolImage())
		return conformance.Fixture{
			Provider: c,
			NewRequest: func(name string) provider.CreateRequest {
				return provider.CreateRequest{Name: name, Region: "hel1", Size: "cpx22"}
			},
			SeedUnmanaged: func(name string) error {
				f.nextID++
				f.servers = append(f.servers, &hcloudgo.Server{ID: f.nextID, Name: name, Status: hcloudgo.ServerStatusRunning})
				return nil
			},
		}
	})
}
