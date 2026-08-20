package hetzner

import (
	"context"
	"errors"
	"strings"
	"testing"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func serverType(name string, locations ...hcloudgo.ServerTypeLocation) *hcloudgo.ServerType {
	return &hcloudgo.ServerType{
		Name:         name,
		Architecture: hcloudgo.ArchitectureX86,
		CPUType:      hcloudgo.CPUTypeShared,
		Cores:        4,
		Memory:       8,
		Disk:         80,
		Locations:    locations,
	}
}

func availableAt(region string) hcloudgo.ServerTypeLocation {
	return hcloudgo.ServerTypeLocation{Location: &hcloudgo.Location{Name: region}, Available: true}
}

func withPricing(st *hcloudgo.ServerType, region, net, currency string) *hcloudgo.ServerType {
	st.Pricings = append(st.Pricings, hcloudgo.ServerTypeLocationPricing{
		Location: &hcloudgo.Location{Name: region},
		Hourly:   hcloudgo.Price{Net: net, Gross: "999.9999", Currency: currency, VATRate: "19"},
	})
	return st
}

func clientWithServerTypes(all []*hcloudgo.ServerType) *Client {
	return NewClientWithAPIs(nil, nil, nil, nil, &fakeServerTypes{all: all}, ServerSpec{})
}

func TestListInstanceTypesRejectsAnEmptyRegion(t *testing.T) {
	c := clientWithServerTypes(nil)
	if _, err := c.ListInstanceTypes(context.Background(), ""); err == nil {
		t.Fatal("ListInstanceTypes must reject an empty region")
	}
}

func TestListInstanceTypesMapsNetPriceNotGross(t *testing.T) {
	st := withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "0.0060", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{st})

	got, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d types, want 1", len(got))
	}
	if got[0].HourlyRate.Amount != 0.0060 {
		t.Errorf("hourly rate amount = %v, want the net price 0.0060", got[0].HourlyRate.Amount)
	}
	if got[0].HourlyRate.Currency != "EUR" {
		t.Errorf("hourly rate currency = %q, want EUR", got[0].HourlyRate.Currency)
	}
}

func TestListInstanceTypesFailsFastOnAMalformedPrice(t *testing.T) {
	st := withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "not-a-number", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{st})

	_, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err == nil {
		t.Fatal("ListInstanceTypes must fail fast on a malformed net price rather than produce a zero rate")
	}
	if !strings.Contains(err.Error(), "cpx22") {
		t.Errorf("error = %v, want it to name the offending server type", err)
	}
}

func TestListInstanceTypesConvertsCoresMemoryAndDisk(t *testing.T) {
	st := withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "0.0060", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{st})

	got, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	row := got[0]
	if row.CPUCores != 4 {
		t.Errorf("CPUCores = %d, want 4", row.CPUCores)
	}
	if row.MemoryBytes != 8*bytesPerGiB {
		t.Errorf("MemoryBytes = %d, want %d", row.MemoryBytes, 8*bytesPerGiB)
	}
	if row.DiskBytes != 80*bytesPerGiB {
		t.Errorf("DiskBytes = %d, want %d", row.DiskBytes, 80*bytesPerGiB)
	}
	if row.Architecture != string(hcloudgo.ArchitectureX86) {
		t.Errorf("Architecture = %q, want %q", row.Architecture, hcloudgo.ArchitectureX86)
	}
	if row.CPUType != string(hcloudgo.CPUTypeShared) {
		t.Errorf("CPUType = %q, want %q", row.CPUType, hcloudgo.CPUTypeShared)
	}
}

func TestListInstanceTypesExcludesOfferingsNotInTheRegion(t *testing.T) {
	inRegion := withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "0.0060", "EUR")
	elsewhere := withPricing(serverType("cpx31", availableAt("fsn1")), "fsn1", "0.0090", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{inRegion, elsewhere})

	got, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if len(got) != 1 || got[0].Name != "cpx22" {
		t.Fatalf("got %+v, want only cpx22 offered in hel1", got)
	}
}

func TestListInstanceTypesSortsResultsByName(t *testing.T) {
	b := withPricing(serverType("cpx31", availableAt("hel1")), "hel1", "0.0090", "EUR")
	a := withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "0.0060", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{b, a})

	got, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if len(got) != 2 || got[0].Name != "cpx22" || got[1].Name != "cpx31" {
		t.Fatalf("got %+v, want sorted by name", got)
	}
}

func TestListInstanceTypesEveryRowCarriesTheRequestedRegion(t *testing.T) {
	st := withPricing(serverType("cpx22", availableAt("hel1")), "hel1", "0.0060", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{st})

	got, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if got[0].Region != "hel1" {
		t.Errorf("Region = %q, want hel1", got[0].Region)
	}
}

func TestListInstanceTypesReflectsAvailabilityAndDeprecationForTheRegion(t *testing.T) {
	loc := hcloudgo.ServerTypeLocation{
		Location:  &hcloudgo.Location{Name: "hel1"},
		Available: false,
		DeprecatableResource: hcloudgo.DeprecatableResource{
			Deprecation: &hcloudgo.DeprecationInfo{},
		},
	}
	st := withPricing(serverType("cpx22", loc), "hel1", "0.0060", "EUR")
	c := clientWithServerTypes([]*hcloudgo.ServerType{st})

	got, err := c.ListInstanceTypes(context.Background(), "hel1")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if got[0].Available {
		t.Error("Available = true, want false for a location reported unavailable")
	}
	if !got[0].Deprecated {
		t.Error("Deprecated = false, want true for a location carrying deprecation info")
	}
}

func TestListInstanceTypesWrapsAServerTypeListingError(t *testing.T) {
	boom := errors.New("boom")
	c := NewClientWithAPIs(nil, nil, nil, nil, &fakeServerTypes{allErr: boom}, ServerSpec{})

	_, err := c.ListInstanceTypes(context.Background(), "hel1")
	if !errors.Is(err, boom) {
		t.Fatalf("ListInstanceTypes error = %v, want it to wrap %v", err, boom)
	}
}

func TestBytesPerGiBIsABinaryGibibyte(t *testing.T) {
	if bytesPerGiB != 1<<30 {
		t.Fatalf("bytesPerGiB = %d, want 1<<30", bytesPerGiB)
	}
}
