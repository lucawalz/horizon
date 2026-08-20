package hetzner

import (
	"testing"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/conformance"
)

func TestHetznerSatisfiesTheProviderContract(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		serverTypes := &fakeServerTypes{all: []*hcloudgo.ServerType{
			withPricing(serverType("cpx31", availableAt("hel1")), "hel1", "0.0090", "EUR"),
			withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "0.0060", "EUR"),
			withPricing(serverType("cpx41", availableAt("fsn1")), "fsn1", "0.0180", "EUR"),
		}}
		c, f := newFakeWithServerTypes(provisionableSpec(), poolImage(), serverTypes)
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
			InstanceTypeRegion:   "hel1",
			ExcludedInstanceType: "cpx41",
		}
	})
}
