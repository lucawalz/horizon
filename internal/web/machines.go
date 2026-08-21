package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	machineTitle     = "horizon machine types"
	configReadFailed = "the provider configs could not be read from the cluster"
	chooseNotice     = "Choose a provider config and name a region to list the instance types it offers."
	absentNotice     = "The instance type catalogue is filled by the operator inside the cluster. A local dashboard holds no copy of it, so no instance types are listed here."
	unfilledNotice   = "The operator has not filled the instance type catalogue for provider config %q yet."
	noMatchNotice    = "Provider config %q offers no instance types in region %q."
	readFailedNotice = "The instance types for provider config %q could not be read: %v."
	refreshedNotice  = "The catalogue for provider config %q was refreshed %s ago."
	configQueryKey   = "config"
	regionQueryKey   = "region"
	rateForm         = "%.4f %s"
)

var errAbsentCatalogue = errors.New("web: this process holds no instance type catalogue")

// a process without a catalogue refresher reports the absence rather than an empty list
type absentCatalogue struct{}

func AbsentCatalogue() catalogue.Reader { return absentCatalogue{} }

func (absentCatalogue) List(string, string) ([]provider.InstanceType, error) {
	return nil, errAbsentCatalogue
}

func (absentCatalogue) Age(string) (time.Duration, bool) { return 0, false }

type machineConfigRow struct {
	Name  string
	Type  string
	Ready string
	Age   string
}

type machineTypeRow struct {
	Name         string
	Architecture string
	CPUType      string
	CPUCores     int
	Memory       string
	Disk         string
	HourlyRate   string
	Available    string
	Deprecated   string
}

type machineView struct {
	Configs   []machineConfigRow
	Config    string
	Region    string
	Notice    string
	Refreshed string
	Types     []machineTypeRow
}

func newMachineConfigRows(configs []v1alpha1.ProviderConfig, now time.Time) []machineConfigRow {
	rows := make([]machineConfigRow, 0, len(configs))
	for i := range configs {
		config := &configs[i]
		rows = append(rows, machineConfigRow{
			Name:  config.Name,
			Type:  config.Spec.Type,
			Ready: conditionStatus(config.Status.Conditions, v1alpha1.ConditionReady),
			Age:   age(&config.CreationTimestamp, now),
		})
	}
	return rows
}

func newMachineTypeRow(offered provider.InstanceType) machineTypeRow {
	return machineTypeRow{
		Name:         offered.Name,
		Architecture: text(offered.Architecture),
		CPUType:      text(offered.CPUType),
		CPUCores:     offered.CPUCores,
		Memory:       bytesQuantity(offered.MemoryBytes),
		Disk:         bytesQuantity(offered.DiskBytes),
		HourlyRate:   rate(offered.HourlyRate),
		Available:    yesNo(offered.Available),
		Deprecated:   yesNo(offered.Deprecated),
	}
}

func rate(hourly provider.Rate) string {
	if hourly.Currency == "" {
		return absent
	}
	return fmt.Sprintf(rateForm, hourly.Amount, hourly.Currency)
}

func yesNo(flag bool) string {
	if flag {
		return "yes"
	}
	return "no"
}

func (s *Server) machineTypes(config, region string) ([]machineTypeRow, string) {
	if config == "" || region == "" {
		return nil, chooseNotice
	}

	offered, err := s.catalogue.List(config, region)
	switch {
	case errors.Is(err, errAbsentCatalogue):
		return nil, absentNotice
	case errors.Is(err, catalogue.ErrUnavailable):
		return nil, fmt.Sprintf(unfilledNotice, config)
	case err != nil:
		return nil, fmt.Sprintf(readFailedNotice, config, err)
	case len(offered) == 0:
		return nil, fmt.Sprintf(noMatchNotice, config, region)
	}

	rows := make([]machineTypeRow, 0, len(offered))
	for _, one := range offered {
		rows = append(rows, newMachineTypeRow(one))
	}
	return rows, ""
}

func (s *Server) refreshed(config string) string {
	if config == "" {
		return ""
	}
	since, filled := s.catalogue.Age(config)
	if !filled {
		return ""
	}
	return fmt.Sprintf(refreshedNotice, config, duration.HumanDuration(since))
}

func (s *Server) machines(block string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var configs v1alpha1.ProviderConfigList
		if err := s.client.List(r.Context(), &configs); err != nil {
			slog.Error("list the provider configs", "error", err)
			s.fail(w, block, http.StatusBadGateway, configReadFailed)
			return
		}

		config := r.URL.Query().Get(configQueryKey)
		region := r.URL.Query().Get(regionQueryKey)
		types, notice := s.machineTypes(config, region)

		s.render(w, machinesPage, block, http.StatusOK, newView(machineTitle, machineView{
			Configs:   newMachineConfigRows(configs.Items, time.Now()),
			Config:    config,
			Region:    region,
			Notice:    notice,
			Refreshed: s.refreshed(config),
			Types:     types,
		}))
	}
}
