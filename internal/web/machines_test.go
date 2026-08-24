package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

const refreshTolerance = 2 * time.Second

func offeredType(name, region string) provider.InstanceType {
	return provider.InstanceType{
		Name:         name,
		Architecture: "x86",
		CPUType:      "shared",
		CPUCores:     2,
		MemoryBytes:  4 << 30,
		DiskBytes:    40 << 30,
		Region:       region,
		Available:    true,
		HourlyRate:   provider.Rate{Amount: 0.0074, Currency: "EUR"},
	}
}

func publishedType(name, region string) v1alpha1.InstanceType {
	return v1alpha1.InstanceType{
		Name:         name,
		Region:       region,
		Architecture: "x86",
		CPUType:      "shared",
		CPUCores:     2,
		MemoryBytes:  4 << 30,
		DiskBytes:    40 << 30,
		HourlyRate:   "0.0074",
		Currency:     "EUR",
		Available:    true,
	}
}

func configPublishing(name string, types ...v1alpha1.InstanceType) v1alpha1.ProviderConfig {
	return v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     v1alpha1.ProviderConfigStatus{InstanceTypes: types},
	}
}

func TestMachinesListsTheProviderConfigs(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createProviderConfig(t, "hetzner")

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/machines")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	configs := decodeBody[machineCatalogueResponse](t, response).Configs
	if len(configs) != 1 {
		t.Fatalf("configs = %d, want 1", len(configs))
	}
	if configs[0].Name != "hetzner" {
		t.Errorf("name = %q, want %q", configs[0].Name, "hetzner")
	}
	if configs[0].Type != v1alpha1.ProviderTypeHetzner {
		t.Errorf("type = %q, want %q", configs[0].Type, v1alpha1.ProviderTypeHetzner)
	}
}

func TestMachinesReportsAnAbsentCatalogue(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/machines?config=hetzner&region=nbg1")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := decodeBody[machineCatalogueResponse](t, response)
	if body.State != stateCatalogueAbsent {
		t.Errorf("state = %q, want %q", body.State, stateCatalogueAbsent)
	}
	if body.Detail != nil {
		t.Errorf("detail = %q, want null", *body.Detail)
	}
}

func TestMachineTypesSeparatesTheCatalogueStates(t *testing.T) {
	unfilled := fmt.Errorf("%w for provider config %q", catalogue.ErrUnavailable, "hetzner")

	for name, testCase := range map[string]struct {
		types   catalogue.Reader
		configs []v1alpha1.ProviderConfig
		config  string
		region  string
		rows    int
		state   catalogueState
		detail  *string
	}{
		"no selection": {types: AbsentCatalogue(), state: stateNoSelection},
		"no catalogue": {types: AbsentCatalogue(), config: "hetzner", region: "nbg1", state: stateCatalogueAbsent},
		"never filled": {types: stubCatalogue{err: unfilled}, config: "hetzner", region: "nbg1", state: stateCatalogueUnfilled},
		"read failed": {
			types: stubCatalogue{err: errors.New("token rejected")}, config: "hetzner", region: "nbg1",
			state: stateReadFailed, detail: ptr("token rejected"),
		},
		"filled no rows": {types: stubCatalogue{filled: true}, config: "hetzner", region: "hel1", state: stateNoMatch},
		"filled": {
			types:  stubCatalogue{types: []provider.InstanceType{offeredType("cx22", "nbg1")}, filled: true},
			config: "hetzner", region: "nbg1", rows: 1, state: stateListed,
		},
		"published": {
			types:   AbsentCatalogue(),
			configs: []v1alpha1.ProviderConfig{configPublishing("hetzner", publishedType("cx22", "nbg1"))},
			config:  "hetzner", region: "nbg1", rows: 1, state: stateListed,
		},
		"published in another region": {
			types:   AbsentCatalogue(),
			configs: []v1alpha1.ProviderConfig{configPublishing("hetzner", publishedType("cx22", "fsn1"))},
			config:  "hetzner", region: "nbg1", state: stateNoMatch,
		},
		"published by another config": {
			types:   stubCatalogue{err: unfilled},
			configs: []v1alpha1.ProviderConfig{configPublishing("other", publishedType("cx22", "nbg1"))},
			config:  "hetzner", region: "nbg1", state: stateCatalogueUnfilled,
		},
		"published nothing yet": {
			types:   stubCatalogue{types: []provider.InstanceType{offeredType("cx22", "nbg1")}, filled: true},
			configs: []v1alpha1.ProviderConfig{configPublishing("hetzner")},
			config:  "hetzner", region: "nbg1", rows: 1, state: stateListed,
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t, failingReader{err: errors.New("unused")}, testCase.types)

			found := server.machineTypes(testCase.configs, testCase.config, testCase.region)

			if len(found.types) != testCase.rows {
				t.Errorf("rows = %d, want %d", len(found.types), testCase.rows)
			}
			if found.state != testCase.state {
				t.Errorf("state = %q, want %q", found.state, testCase.state)
			}
			if testCase.detail == nil {
				if found.detail != nil {
					t.Errorf("detail = %q, want null", *found.detail)
				}
				return
			}
			if detail := present(t, "detail", found.detail); !strings.Contains(detail, *testCase.detail) {
				t.Errorf("detail = %q, want it to carry %q", detail, *testCase.detail)
			}
		})
	}
}

func TestMachinesRendersTheOfferedTypes(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createProviderConfig(t, "hetzner")
	types := stubCatalogue{
		types:  []provider.InstanceType{offeredType("cx22", "nbg1")},
		age:    30 * time.Minute,
		filled: true,
	}

	server := newTestServer(t, testEnv.Client, types)
	response := get(t, server, "/api/machines?config=hetzner&region=nbg1")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := decodeBody[machineCatalogueResponse](t, response)
	if body.State != stateListed {
		t.Fatalf("state = %q, want %q", body.State, stateListed)
	}
	if body.Config != "hetzner" {
		t.Errorf("config = %q, want the requested %q echoed back", body.Config, "hetzner")
	}
	if body.Region != "nbg1" {
		t.Errorf("region = %q, want the requested %q echoed back", body.Region, "nbg1")
	}
	if len(body.Types) != 1 {
		t.Fatalf("types = %d, want 1", len(body.Types))
	}

	offered := body.Types[0]
	if offered.Name != "cx22" {
		t.Errorf("name = %q, want %q", offered.Name, "cx22")
	}
	if architecture := present(t, "architecture", offered.Architecture); architecture != "x86" {
		t.Errorf("architecture = %q, want %q", architecture, "x86")
	}
	if cpuType := present(t, "cpuType", offered.CPUType); cpuType != "shared" {
		t.Errorf("cpuType = %q, want %q", cpuType, "shared")
	}
	if offered.CPUCores != 2 {
		t.Errorf("cpuCores = %d, want 2", offered.CPUCores)
	}
	if offered.MemoryBytes != 4<<30 {
		t.Errorf("memoryBytes = %d, want %d", offered.MemoryBytes, 4<<30)
	}
	if offered.DiskBytes != 40<<30 {
		t.Errorf("diskBytes = %d, want %d", offered.DiskBytes, 40<<30)
	}
	if !offered.Available {
		t.Error("available = false, want true")
	}
	if offered.Deprecated {
		t.Error("deprecated = true, want false")
	}

	hourly := present(t, "hourlyRate", offered.HourlyRate)
	if hourly.Amount != 0.0074 {
		t.Errorf("hourlyRate amount = %v, want %v", hourly.Amount, 0.0074)
	}
	if hourly.Currency != "EUR" {
		t.Errorf("hourlyRate currency = %q, want %q", hourly.Currency, "EUR")
	}

	refreshedAt := parseInstant(t, "refreshedAt", present(t, "refreshedAt", body.RefreshedAt))
	want := time.Now().Add(-30 * time.Minute)
	if drift := refreshedAt.Sub(want); drift > refreshTolerance || drift < -refreshTolerance {
		t.Errorf("refreshedAt = %s, want %s within %s", refreshedAt, want, refreshTolerance)
	}
}

func TestMachinesRendersTheTypesTheControllerPublished(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	config := createProviderConfig(t, "hetzner")
	config.Status.InstanceTypes = []v1alpha1.InstanceType{publishedType("cx22", "nbg1")}
	if err := testEnv.Client.Status().Update(t.Context(), config); err != nil {
		t.Fatalf("publish the catalogue: %v", err)
	}

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/machines?config=hetzner&region=nbg1")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := decodeBody[machineCatalogueResponse](t, response)
	if body.State != stateListed {
		t.Fatalf("state = %q, want %q", body.State, stateListed)
	}
	if len(body.Types) != 1 {
		t.Fatalf("types = %d, want 1", len(body.Types))
	}

	offered := body.Types[0]
	if offered.Name != "cx22" || offered.CPUCores != 2 || offered.MemoryBytes != 4<<30 || offered.DiskBytes != 40<<30 {
		t.Errorf("offered = %+v, want the published row", offered)
	}
	if architecture := present(t, "architecture", offered.Architecture); architecture != "x86" {
		t.Errorf("architecture = %q, want %q", architecture, "x86")
	}
	if cpuType := present(t, "cpuType", offered.CPUType); cpuType != "shared" {
		t.Errorf("cpuType = %q, want %q", cpuType, "shared")
	}
	hourly := present(t, "hourlyRate", offered.HourlyRate)
	if hourly.Amount != 0.0074 || hourly.Currency != "EUR" {
		t.Errorf("hourlyRate = %+v, want 0.0074 EUR", hourly)
	}
}

func TestMachinesReportsAClusterFailure(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("the api server is unreachable")}, AbsentCatalogue())

	response := get(t, server, "/api/machines")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if failure := decodeBody[apiError](t, response); failure.Status != http.StatusBadGateway {
		t.Errorf("body status = %d, want %d", failure.Status, http.StatusBadGateway)
	}
}

func TestRefreshedReportsTheCatalogueFetchInstant(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	fetched := now.Add(-30 * time.Minute)

	for name, testCase := range map[string]struct {
		types  catalogue.Reader
		config string
		want   *string
	}{
		"no selection": {types: stubCatalogue{age: 30 * time.Minute, filled: true}},
		"never filled": {types: stubCatalogue{age: 30 * time.Minute}, config: "hetzner"},
		"filled": {
			types: stubCatalogue{age: 30 * time.Minute, filled: true}, config: "hetzner",
			want: ptr(fetched.Format(time.RFC3339)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t, failingReader{err: errors.New("unused")}, testCase.types)

			refreshedAt := server.refreshed(testCase.config, now)
			if testCase.want == nil {
				if refreshedAt != nil {
					t.Errorf("refreshedAt = %q, want null for a catalogue that reports no fetch", *refreshedAt)
				}
				return
			}
			if got := present(t, "refreshedAt", refreshedAt); got != *testCase.want {
				t.Errorf("refreshedAt = %q, want %q", got, *testCase.want)
			}
		})
	}
}
