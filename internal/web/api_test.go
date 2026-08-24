package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	zeroInstantJSON = `"0001-01-01T00:00:00Z"`
	zeroSummaryJSON = `{"name":"","replicas":0,"region":"","phase":null,"expiresAt":null,"ready":null,` +
		`"armed":null,"createdAt":` + zeroInstantJSON + `,"instanceType":null,"readyAt":null,"releasedAt":null}`
	zeroSelectionJSON = `{"strategy":"","chosen":"","hourlyRate":null,"currency":null,"runnerUp":null,` +
		`"offered":0,"qualified":0,"rejected":[],"decidedAt":` + zeroInstantJSON + `}`
	zeroDetailJSON = `{"summary":` + zeroSummaryJSON + `,"providerRef":"","size":null,"requirements":null,` +
		`"selection":null,"durationSeconds":0,"teardownGraceSeconds":null,"workloadNamespace":null,"migratedWorkloads":[],` +
		`"migrationWarnings":[],` +
		`"acceptedAt":null,"backstopAt":null,"watchdogDeadline":null,"observedGeneration":0,"conditions":[],"instances":[],` +
		`"observedAt":"2026-08-21T12:00:00Z"}`
	zeroCatalogueJSON = `{"configs":[],"config":"","region":"","state":"NoSelection","detail":null,` +
		`"refreshedAt":null,"types":[],"observedAt":"2026-08-21T12:00:00Z"}`
	zeroMachineTypeJSON = `{"name":"","architecture":null,"cpuType":null,"cpuCores":0,"memoryBytes":0,` +
		`"diskBytes":0,"hourlyRate":null,"available":false,"deprecated":false}`
)

func TestZeroValuedResponsesEncodeNullsAndEmptyLists(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	for name, testCase := range map[string]struct {
		body any
		want string
	}{
		"lease list":        {newLeaseListResponse(nil, now), `{"leases":[],"observedAt":"2026-08-21T12:00:00Z"}`},
		"lease summary":     {newLeaseSummary(&v1alpha1.CapacityLease{}), zeroSummaryJSON},
		"lease detail":      {newLeaseDetailResponse(&v1alpha1.CapacityLease{}, now), zeroDetailJSON},
		"machine catalogue": {server.newMachineCatalogueResponse(nil, "", "", now), zeroCatalogueJSON},
		"condition": {
			newConditionEntries([]metav1.Condition{{}})[0],
			`{"type":"","status":"","reason":null,"message":null,"lastTransitionTime":` + zeroInstantJSON + `}`,
		},
		"lease instance": {
			newLeaseInstances([]v1alpha1.InstanceStatus{{}})[0],
			`{"name":"","providerID":null,"nodeName":null,"phase":"","stage":null,"createdAt":null,"lastError":null}`,
		},
		"lease selection": {newLeaseSelection(&v1alpha1.SelectionStatus{}), zeroSelectionJSON},
		"lease requirements": {
			newLeaseRequirements(&v1alpha1.SizeRequirements{}),
			`{"minCPU":0,"minMemory":null,"architecture":"","cpuType":null,"strategy":null}`,
		},
		"provider config": {
			newProviderConfigSummaries([]v1alpha1.ProviderConfig{{}})[0],
			`{"name":"","type":"","ready":null,"cataloguePublished":null,"createdAt":` + zeroInstantJSON + `}`,
		},
		"machine type": {newMachineType(provider.InstanceType{}), zeroMachineTypeJSON},
		"money":        {money{}, `{"amount":0,"currency":""}`},
		"failure": {
			apiError{Status: http.StatusBadGateway, Title: http.StatusText(http.StatusBadGateway), Detail: leaseReadFailed},
			`{"status":502,"title":"Bad Gateway","detail":"` + leaseReadFailed + `"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(testCase.body)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if string(encoded) != testCase.want {
				t.Errorf("encoded\n%s\nwant\n%s", encoded, testCase.want)
			}
		})
	}
}

func TestJSONResponsesAllowNoCrossOriginCaller(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, "/api/leases")
	sent := sentHeaders(response)
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"} {
		if got := sent.Get(header); got != "" {
			t.Errorf("%s = %q, want no cross-origin allowance on a loopback interface", header, got)
		}
	}
}
