package web

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	configReadFailed = "the provider configs could not be read from the cluster"
	configQueryKey   = "config"
	regionQueryKey   = "region"
)

type catalogueState string

const (
	stateNoSelection       catalogueState = "NoSelection"
	stateCatalogueAbsent   catalogueState = "CatalogueAbsent"
	stateCatalogueUnfilled catalogueState = "CatalogueUnfilled"
	stateNoMatch           catalogueState = "NoMatch"
	stateReadFailed        catalogueState = "ReadFailed"
	stateListed            catalogueState = "Listed"
)

var errAbsentCatalogue = errors.New("web: this process holds no instance type catalogue")

// a process without a catalogue refresher reports the absence rather than an empty list
type absentCatalogue struct{}

func AbsentCatalogue() catalogue.Reader { return absentCatalogue{} }

func (absentCatalogue) List(string, string) ([]provider.InstanceType, error) {
	return nil, errAbsentCatalogue
}

func (absentCatalogue) Age(string) (time.Duration, bool) { return 0, false }

type providerConfigSummary struct {
	Name      string                  `json:"name"`
	Type      string                  `json:"type"`
	Ready     *metav1.ConditionStatus `json:"ready"`
	CreatedAt string                  `json:"createdAt"`
}

type money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type machineType struct {
	Name         string  `json:"name"`
	Architecture *string `json:"architecture"`
	CPUType      *string `json:"cpuType"`
	CPUCores     int     `json:"cpuCores"`
	MemoryBytes  int64   `json:"memoryBytes"`
	DiskBytes    int64   `json:"diskBytes"`
	HourlyRate   *money  `json:"hourlyRate"`
	Available    bool    `json:"available"`
	Deprecated   bool    `json:"deprecated"`
}

type machineCatalogueResponse struct {
	Configs     []providerConfigSummary `json:"configs"`
	Config      string                  `json:"config"`
	Region      string                  `json:"region"`
	State       catalogueState          `json:"state"`
	Detail      *string                 `json:"detail"`
	RefreshedAt *string                 `json:"refreshedAt"`
	Types       []machineType           `json:"types"`
	ObservedAt  string                  `json:"observedAt"`
}

type catalogueResult struct {
	types  []machineType
	state  catalogueState
	detail *string
}

func newProviderConfigSummaries(configs []v1alpha1.ProviderConfig) []providerConfigSummary {
	summaries := make([]providerConfigSummary, 0, len(configs))
	for i := range configs {
		config := &configs[i]
		summaries = append(summaries, providerConfigSummary{
			Name:      config.Name,
			Type:      config.Spec.Type,
			Ready:     conditionStatus(config.Status.Conditions, v1alpha1.ConditionReady),
			CreatedAt: rfc3339(config.CreationTimestamp.Time),
		})
	}
	return summaries
}

func newMachineType(offered provider.InstanceType) machineType {
	return machineType{
		Name:         offered.Name,
		Architecture: nullable(offered.Architecture),
		CPUType:      nullable(offered.CPUType),
		CPUCores:     offered.CPUCores,
		MemoryBytes:  offered.MemoryBytes,
		DiskBytes:    offered.DiskBytes,
		HourlyRate:   rate(offered.HourlyRate),
		Available:    offered.Available,
		Deprecated:   offered.Deprecated,
	}
}

func rate(hourly provider.Rate) *money {
	if hourly.Currency == "" {
		return nil
	}
	return &money{Amount: hourly.Amount, Currency: hourly.Currency}
}

func (s *Server) machineTypes(config, region string) catalogueResult {
	if config == "" || region == "" {
		return catalogueResult{state: stateNoSelection}
	}

	offered, err := s.catalogue.List(config, region)
	switch {
	case errors.Is(err, errAbsentCatalogue):
		return catalogueResult{state: stateCatalogueAbsent}
	case errors.Is(err, catalogue.ErrUnavailable):
		return catalogueResult{state: stateCatalogueUnfilled}
	case err != nil:
		return catalogueResult{state: stateReadFailed, detail: ptr(err.Error())}
	case len(offered) == 0:
		return catalogueResult{state: stateNoMatch}
	}

	types := make([]machineType, 0, len(offered))
	for _, one := range offered {
		types = append(types, newMachineType(one))
	}
	return catalogueResult{types: types, state: stateListed}
}

func (s *Server) refreshed(config string, now time.Time) *string {
	if config == "" {
		return nil
	}
	since, filled := s.catalogue.Age(config)
	if !filled {
		return nil
	}
	return ptr(rfc3339(now.Add(-since)))
}

func (s *Server) newMachineCatalogueResponse(
	configs []v1alpha1.ProviderConfig, config, region string, now time.Time,
) machineCatalogueResponse {
	found := s.machineTypes(config, region)
	return machineCatalogueResponse{
		Configs:     newProviderConfigSummaries(configs),
		Config:      config,
		Region:      region,
		State:       found.state,
		Detail:      found.detail,
		RefreshedAt: s.refreshed(config, now),
		Types:       orEmpty(found.types),
		ObservedAt:  rfc3339(now),
	}
}

func (s *Server) machines(w http.ResponseWriter, r *http.Request) {
	reader, held := requestClient(w, r, s.readers)
	if !held {
		return
	}

	var configs v1alpha1.ProviderConfigList
	if err := reader.List(r.Context(), &configs); err != nil {
		if refusedByAuthorisation(w, r, err) {
			return
		}
		slog.Error("list the provider configs", "error", err)
		writeAPIError(w, http.StatusBadGateway, configReadFailed)
		return
	}

	config := r.URL.Query().Get(configQueryKey)
	region := r.URL.Query().Get(regionQueryKey)
	writeJSON(w, http.StatusOK, s.newMachineCatalogueResponse(configs.Items, config, region, time.Now()))
}
