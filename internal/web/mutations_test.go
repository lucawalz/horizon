package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	gibibyte         = 4 * 1024 * 1024 * 1024
	gigabyte         = 4_000_000_000
	leasesEndpoint   = "/api/leases"
	machinesEndpoint = "/api/machines"
)

func leaseEndpoint(name string) string { return leasesEndpoint + "/" + name }

func newWritingServer(t *testing.T) *Server {
	t.Helper()
	server, err := New(Options{Client: testEnv.Client, Writer: testEnv.Client, Catalogue: AbsentCatalogue()})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func newMutation(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the request body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	request := httptest.NewRequest(method, target, payload)
	request.Header.Set(interfaceHeader, "true")
	request.Header.Set(fetchSiteHeader, sameOriginSite)
	request.Header.Set(originHeader, httpScheme+request.Host)
	return request
}

func send(server *Server, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)
	return recorder
}

func mutate(t *testing.T, server *Server, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return send(server, newMutation(t, method, target, body))
}

func readLease(t *testing.T, name string) *v1alpha1.CapacityLease {
	t.Helper()
	var lease v1alpha1.CapacityLease
	if err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: name}, &lease); err != nil {
		t.Fatalf("read lease %s: %v", name, err)
	}
	return &lease
}

func createRequestFixture(name string) leaseCreateRequest {
	return leaseCreateRequest{
		Name:        name,
		ProviderRef: "hetzner",
		Region:      "nbg1",
		Requirements: &leaseRequirementsRequest{
			MinCPU:       4,
			MinMemory:    "4G",
			Architecture: string(v1alpha1.ArchitectureX86),
			CPUType:      string(v1alpha1.CPUTypeShared),
			Strategy:     string(v1alpha1.StrategyLowestPricePerCore),
		},
		Replicas:             2,
		DurationSeconds:      seconds(90 * time.Minute),
		TeardownGraceSeconds: ptr(int64(30)),
		WorkloadNamespace:    "batch",
	}
}

func TestLeaseCreateStoresExactlyTheSubmittedFields(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "created-run"
	removeAfterTest(t, name)

	server := newWritingServer(t)
	response := mutate(t, server, http.MethodPost, leasesEndpoint, createRequestFixture(name))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusCreated, response.Body)
	}
	if named := decodeBody[leaseDetailResponse](t, response).Summary.Name; named != name {
		t.Errorf("the response names %q, want %q", named, name)
	}

	spec := readLease(t, name).Spec
	if spec.ProviderRef != "hetzner" {
		t.Errorf("providerRef = %q, want %q", spec.ProviderRef, "hetzner")
	}
	if spec.Region != "nbg1" {
		t.Errorf("region = %q, want %q", spec.Region, "nbg1")
	}
	if spec.Size != "" {
		t.Errorf("size = %q, want it unset", spec.Size)
	}
	if spec.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", spec.Replicas)
	}
	if spec.Duration.Duration != 90*time.Minute {
		t.Errorf("duration = %s, want 90m", spec.Duration.Duration)
	}
	if grace := present(t, "teardownGrace", spec.TeardownGrace); grace.Duration != 30*time.Second {
		t.Errorf("teardownGrace = %s, want 30s", grace.Duration)
	}
	if workload := present(t, "workload", spec.Workload); workload.Namespace != "batch" {
		t.Errorf("workload namespace = %q, want %q", workload.Namespace, "batch")
	}

	requirements := present(t, "requirements", spec.Requirements)
	if requirements.MinCPU != 4 {
		t.Errorf("minCPU = %d, want 4", requirements.MinCPU)
	}
	if requirements.Architecture != v1alpha1.ArchitectureX86 {
		t.Errorf("architecture = %q, want %q", requirements.Architecture, v1alpha1.ArchitectureX86)
	}
	if requirements.CPUType != v1alpha1.CPUTypeShared {
		t.Errorf("cpuType = %q, want %q", requirements.CPUType, v1alpha1.CPUTypeShared)
	}
	if requirements.Strategy != v1alpha1.StrategyLowestPricePerCore {
		t.Errorf("strategy = %q, want %q", requirements.Strategy, v1alpha1.StrategyLowestPricePerCore)
	}
	if memory := present(t, "minMemory", requirements.MinMemory); memory.Value() != gigabyte {
		t.Errorf("minMemory = %d bytes, want %d", memory.Value(), int64(gigabyte))
	}
}

// a decimal suffix and a binary one differ by 7 percent, which is the difference between offering cx23 and excluding it
func TestLeaseCreateKeepsTheSubmittedMemoryUnit(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	for name, testCase := range map[string]struct {
		quantity string
		want     int64
	}{
		"decimal": {quantity: "4G", want: gigabyte},
		"binary":  {quantity: "4Gi", want: gibibyte},
	} {
		t.Run(name, func(t *testing.T) {
			lease := "memory-" + name
			removeAfterTest(t, lease)

			request := createRequestFixture(lease)
			request.Requirements.MinMemory = testCase.quantity

			response := mutate(t, newWritingServer(t), http.MethodPost, leasesEndpoint, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusCreated, response.Body)
			}

			stored := present(t, "minMemory", readLease(t, lease).Spec.Requirements.MinMemory)
			if stored.Value() != testCase.want {
				t.Errorf("minMemory %q stored %d bytes, want %d", testCase.quantity, stored.Value(), testCase.want)
			}
		})
	}
}

func TestLeaseCreateSurfacesTheRefusalOfSizeAndRequirementsTogether(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "both-run"
	removeAfterTest(t, name)

	request := createRequestFixture(name)
	request.Size = "cx22"

	response := mutate(t, newWritingServer(t), http.MethodPost, leasesEndpoint, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}
	if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, "exactly one of size or requirements") {
		t.Errorf("detail = %q, want the message the apiserver rejected it with", detail)
	}
}

func TestLeaseCreateRefusesValuesOutsideTheirBounds(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	for name, adjust := range map[string]func(*leaseCreateRequest){
		"no replicas":       func(r *leaseCreateRequest) { r.Replicas = 0 },
		"too many replicas": func(r *leaseCreateRequest) { r.Replicas = 9 },
		"duration too short": func(r *leaseCreateRequest) {
			r.DurationSeconds = seconds(4 * time.Minute)
		},
		"duration too long": func(r *leaseCreateRequest) {
			r.DurationSeconds = seconds(9 * time.Hour)
		},
		"grace too long": func(r *leaseCreateRequest) {
			r.TeardownGraceSeconds = ptr(seconds(16 * time.Minute))
		},
		"no cores":   func(r *leaseCreateRequest) { r.Requirements.MinCPU = 0 },
		"many cores": func(r *leaseCreateRequest) { r.Requirements.MinCPU = 65 },
	} {
		t.Run(name, func(t *testing.T) {
			lease := "bounds-run"
			removeAfterTest(t, lease)

			request := createRequestFixture(lease)
			adjust(&request)

			response := mutate(t, newWritingServer(t), http.MethodPost, leasesEndpoint, request)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusUnprocessableEntity, response.Body)
			}
			if detail := decodeBody[apiError](t, response).Detail; detail == "" {
				t.Error("the refusal carried no detail, want the message the apiserver rejected it with")
			}
		})
	}
}

func TestLeaseCreateRefusesAQuantityItCannotRead(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	request := createRequestFixture("unreadable-run")
	request.Requirements.MinMemory = "four gigs"

	response := mutate(t, newWritingServer(t), http.MethodPost, leasesEndpoint, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusBadRequest, response.Body)
	}
}

func TestLeaseReleaseAsksForTheNamedLeaseAlone(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	now := time.Now()
	createLease(t, leaseFixture("released-run"), activeStatus(now))
	createLease(t, leaseFixture("kept-run"), activeStatus(now))

	response := mutate(t, newWritingServer(t), http.MethodDelete, leaseEndpoint("released-run"), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusAccepted, response.Body)
	}
	if named := decodeBody[leaseReleaseResponse](t, response).Name; named != "released-run" {
		t.Errorf("the response names %q, want %q", named, "released-run")
	}

	var released v1alpha1.CapacityLease
	err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: "released-run"}, &released)
	if !apierrors.IsNotFound(err) {
		t.Errorf("reading the released lease answered %v, want a not-found", err)
	}
	if kept := readLease(t, "kept-run"); kept.DeletionTimestamp != nil {
		t.Error("the lease that was not named is being deleted too")
	}
}

func TestLeaseReleaseAnswersNotFoundForAnAbsentLease(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	response := mutate(t, newWritingServer(t), http.MethodDelete, leaseEndpoint("never-existed"), nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusNotFound, response.Body)
	}
}

func TestLeaseDetailAndReleaseDoNotShadowEachOther(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("both-methods"), activeStatus(time.Now()))
	server := newWritingServer(t)

	read := get(t, server, leaseEndpoint("both-methods"))
	if read.Code != http.StatusOK {
		t.Fatalf("reading answered %d, want %d, body %s", read.Code, http.StatusOK, read.Body)
	}
	if named := decodeBody[leaseDetailResponse](t, read).Summary.Name; named != "both-methods" {
		t.Errorf("reading named %q, want %q", named, "both-methods")
	}

	released := mutate(t, server, http.MethodDelete, leaseEndpoint("both-methods"), nil)
	if released.Code != http.StatusAccepted {
		t.Fatalf("releasing answered %d, want %d, body %s", released.Code, http.StatusAccepted, released.Body)
	}
}

func TestMutationsRefuseEachCrossOriginFailureOnItsOwn(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newWritingServer(t)
	for name, spoil := range map[string]func(*http.Request){
		"no interface header": func(r *http.Request) { r.Header.Del(interfaceHeader) },
		"no fetch metadata":   func(r *http.Request) { r.Header.Del(fetchSiteHeader) },
		"cross site fetch":    func(r *http.Request) { r.Header.Set(fetchSiteHeader, "cross-site") },
		"foreign origin":      func(r *http.Request) { r.Header.Set(originHeader, "http://evil.example") },
	} {
		t.Run(name, func(t *testing.T) {
			for method, target := range map[string]string{
				http.MethodPost:   leasesEndpoint,
				http.MethodDelete: leaseEndpoint("guarded-run"),
			} {
				request := newMutation(t, method, target, createRequestFixture("guarded-run"))
				spoil(request)

				response := send(server, request)
				if response.Code != http.StatusForbidden {
					t.Errorf("%s answered %d, want %d, body %s", method, response.Code, http.StatusForbidden, response.Body)
				}
			}

			var lease v1alpha1.CapacityLease
			err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: "guarded-run"}, &lease)
			if !apierrors.IsNotFound(err) {
				t.Errorf("reading the refused lease answered %v, want a not-found", err)
			}
		})
	}
}

func TestMutationsAnswerNoCorsHeaders(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "cors-run"
	removeAfterTest(t, name)

	response := mutate(t, newWritingServer(t), http.MethodPost, leasesEndpoint, createRequestFixture(name))
	for _, header := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Credentials",
	} {
		if value := sentHeaders(response).Get(header); value != "" {
			t.Errorf("%s = %q, want the browser to have nothing to relax the same-origin rule with", header, value)
		}
	}
}

func TestAServerWithoutAWriterServesReadsAndRefusesMutations(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("read-only-run"), activeStatus(time.Now()))
	server := newTestServer(t, testEnv.Client, AbsentCatalogue())

	for name, target := range map[string]string{
		"lease list":   leasesEndpoint,
		"lease detail": leaseEndpoint("read-only-run"),
		"machines":     machinesEndpoint,
	} {
		t.Run(name, func(t *testing.T) {
			if response := get(t, server, target); response.Code != http.StatusOK {
				t.Errorf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
			}
		})
	}

	for method, target := range map[string]string{
		http.MethodPost:   leasesEndpoint,
		http.MethodDelete: leaseEndpoint("read-only-run"),
	} {
		response := mutate(t, server, method, target, createRequestFixture("read-only-run"))
		if response.Code != http.StatusNotImplemented {
			t.Fatalf("%s answered %d, want %d, body %s", method, response.Code, http.StatusNotImplemented, response.Body)
		}
		if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, "read-only") {
			t.Errorf("%s detail = %q, want it to name the read-only interface", method, detail)
		}
	}

	if err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: "read-only-run"}, &v1alpha1.CapacityLease{}); err != nil {
		t.Errorf("the lease a read-only interface was asked to release is gone: %v", err)
	}
}

func TestNewBuildsAReadOnlyServerWithoutAWriter(t *testing.T) {
	if _, err := New(Options{Client: failingReader{err: errors.New("unused")}, Catalogue: AbsentCatalogue()}); err != nil {
		t.Errorf("building a server without a writer failed with %v, want a read-only server", err)
	}
}
