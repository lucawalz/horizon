package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

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

func TestMachinesListsTheProviderConfigs(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createProviderConfig(t, "hetzner")

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/machines")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "hetzner") {
		t.Error("the machine view omits the provider config")
	}
}

func TestMachinesReportsAnAbsentCatalogue(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/machines?config=hetzner&region=nbg1")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "holds no copy of it") {
		t.Error("the absent catalogue is not reported")
	}
}

func TestMachineTypesSeparatesTheCatalogueStates(t *testing.T) {
	unfilled := fmt.Errorf("%w for provider config %q", catalogue.ErrUnavailable, "hetzner")

	for name, testCase := range map[string]struct {
		types  catalogue.Reader
		config string
		region string
		rows   int
		notice string
	}{
		"no selection":   {types: AbsentCatalogue(), rows: 0, notice: chooseNotice},
		"no catalogue":   {types: AbsentCatalogue(), config: "hetzner", region: "nbg1", notice: absentNotice},
		"never filled":   {types: stubCatalogue{err: unfilled}, config: "hetzner", region: "nbg1", notice: fmt.Sprintf(unfilledNotice, "hetzner")},
		"read failed":    {types: stubCatalogue{err: errors.New("token rejected")}, config: "hetzner", region: "nbg1", notice: fmt.Sprintf(readFailedNotice, "hetzner", errors.New("token rejected"))},
		"filled no rows": {types: stubCatalogue{filled: true}, config: "hetzner", region: "hel1", notice: fmt.Sprintf(noMatchNotice, "hetzner", "hel1")},
		"filled": {
			types:  stubCatalogue{types: []provider.InstanceType{offeredType("cx22", "nbg1")}, filled: true},
			config: "hetzner", region: "nbg1", rows: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t, failingReader{err: errors.New("unused")}, testCase.types)

			rows, notice := server.machineTypes(testCase.config, testCase.region)

			if len(rows) != testCase.rows {
				t.Errorf("rows = %d, want %d", len(rows), testCase.rows)
			}
			if notice != testCase.notice {
				t.Errorf("notice = %q, want %q", notice, testCase.notice)
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
	response := get(t, server, "/machines?config=hetzner&region=nbg1")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{"cx22", "4Gi", "0.0074 EUR", "refreshed 30m ago"} {
		if !strings.Contains(body, want) {
			t.Errorf("the machine view omits %q", want)
		}
	}
}

func TestMachinesReportsAClusterFailure(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("the api server is unreachable")}, AbsentCatalogue())

	if response := get(t, server, "/machines"); response.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
}
