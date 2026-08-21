package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	zeroSummaryJSON = `{"name":"","replicas":0,"region":"","phase":null,"expiresAt":null,"ready":null,` +
		`"armed":null,"createdAt":"0001-01-01T00:00:00Z","instanceType":null,"readyAt":null,"releasedAt":null}`
	zeroDetailJSON = `{"summary":` + zeroSummaryJSON + `,"providerRef":"","size":null,"requirements":null,` +
		`"durationSeconds":0,"teardownGraceSeconds":null,"workloadNamespace":null,"migratedWorkloads":[],` +
		`"acceptedAt":null,"watchdogDeadline":null,"observedGeneration":0,"conditions":[],"instances":[],` +
		`"observedAt":"2026-08-21T12:00:00Z"}`
	zeroCatalogueJSON = `{"configs":[],"config":"","region":"","state":"NoSelection","detail":null,` +
		`"refreshedAt":null,"types":[],"observedAt":"2026-08-21T12:00:00Z"}`
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
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"} {
		if got := response.Header().Get(header); got != "" {
			t.Errorf("%s = %q, want no cross-origin allowance on a loopback interface", header, got)
		}
	}
}
