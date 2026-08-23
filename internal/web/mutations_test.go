package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func writingOptions() Options {
	return Options{Client: testEnv.Client, Writer: testEnv.Client, Catalogue: AbsentCatalogue()}
}

func newWritingServer(t *testing.T) *Server {
	t.Helper()
	server, err := New(writingOptions())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)
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
	request.Host = servedTestHost
	request.Header.Set(interfaceHeader, "true")
	request.Header.Set(fetchSiteHeader, sameOriginSite)
	request.Header.Set(originHeader, servedTestOrigin)
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
		"grace below zero": func(r *leaseCreateRequest) {
			r.TeardownGraceSeconds = ptr(seconds(-1 * time.Second))
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
		// a rebound name reaches this socket while the browser still calls the request same-origin, so every other signal it carries is genuine
		"rebound host": func(r *http.Request) {
			r.Host = "evil.example:8973"
			r.Header.Set(originHeader, "http://evil.example:8973")
		},
		"another port on the loopback address": func(r *http.Request) {
			r.Host = "127.0.0.1:5173"
			r.Header.Set(originHeader, "http://127.0.0.1:5173")
		},
		"no interface header": func(r *http.Request) { r.Header.Del(interfaceHeader) },
		"no fetch metadata":   func(r *http.Request) { r.Header.Del(fetchSiteHeader) },
		"cross site fetch":    func(r *http.Request) { r.Header.Set(fetchSiteHeader, "cross-site") },
		"foreign origin":      func(r *http.Request) { r.Header.Set(originHeader, "http://evil.example") },
	} {
		t.Run(name, func(t *testing.T) {
			// a name shared across the subtests lets whichever one a regression admits fail the rest, and the point of the table is that each check answers for itself
			refused := "guarded-" + strings.ReplaceAll(name, " ", "-")
			removeAfterTest(t, refused)

			for method, target := range map[string]string{
				http.MethodPost:   leasesEndpoint,
				http.MethodDelete: leaseEndpoint(refused),
			} {
				request := newMutation(t, method, target, createRequestFixture(refused))
				spoil(request)

				response := send(server, request)
				if response.Code != http.StatusForbidden {
					t.Errorf("%s answered %d, want %d, body %s", method, response.Code, http.StatusForbidden, response.Body)
				}
			}

			var lease v1alpha1.CapacityLease
			err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: refused}, &lease)
			if !apierrors.IsNotFound(err) {
				t.Errorf("reading the refused lease answered %v, want a not-found", err)
			}
		})
	}
}

const (
	externalTestHost   = "horizon.example"
	externalTestOrigin = "https://" + externalTestHost
)

func externalOptions() Options {
	auth := completeAuthentication()
	auth.ExternalOrigin = externalTestOrigin

	options := writingOptions()
	options.Authentication = &auth
	return options
}

func newExternalServer(t *testing.T) *Server {
	t.Helper()
	server, err := New(externalOptions())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)
	return server
}

func newExternalMutation(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	request := newMutation(t, method, target, body)
	request.Host = externalTestHost
	request.Header.Set(originHeader, externalTestOrigin)
	return request
}

func TestMutationsThroughTheConfiguredExternalOriginAreServed(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "proxied-run"
	removeAfterTest(t, name)

	response := send(newExternalServer(t), newExternalMutation(t, http.MethodPost, leasesEndpoint, createRequestFixture(name)))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusCreated, response.Body)
	}
	readLease(t, name)
}

func TestMutationsBehindAProxyRefuseEachCrossOriginFailureOnItsOwn(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newExternalServer(t)
	for name, spoil := range map[string]func(*http.Request){
		// a name whose zone the caller owns can be pointed at the pod, and the browser calls that request same-origin too
		"rebound name reaching the pod": func(r *http.Request) {
			r.Host = "evil.example"
			r.Header.Set(originHeader, "https://evil.example")
		},
		"rebound name carrying the served origin": func(r *http.Request) {
			r.Host = "evil.example"
		},
		// anything that can reach the pod directly writes these itself, so a decision must never rest on them
		"forwarded headers naming the served origin": func(r *http.Request) {
			r.Host = "evil.example"
			r.Header.Set("X-Forwarded-Host", externalTestHost)
			r.Header.Set("X-Forwarded-Proto", "https")
		},
		"the loopback origin the dashboard serves": func(r *http.Request) {
			r.Host = servedTestHost
			r.Header.Set(originHeader, servedTestOrigin)
		},
		"origin of another host": func(r *http.Request) {
			r.Header.Set(originHeader, "https://evil.example")
		},
		"origin differing only by scheme": func(r *http.Request) {
			r.Header.Set(originHeader, httpScheme+externalTestHost)
		},
		"no interface header": func(r *http.Request) { r.Header.Del(interfaceHeader) },
		"no fetch metadata":   func(r *http.Request) { r.Header.Del(fetchSiteHeader) },
		"cross site fetch":    func(r *http.Request) { r.Header.Set(fetchSiteHeader, "cross-site") },
	} {
		t.Run(name, func(t *testing.T) {
			refused := "proxied-" + strings.ReplaceAll(name, " ", "-")
			removeAfterTest(t, refused)

			for method, target := range map[string]string{
				http.MethodPost:   leasesEndpoint,
				http.MethodDelete: leaseEndpoint(refused),
			} {
				request := newExternalMutation(t, method, target, createRequestFixture(refused))
				spoil(request)

				response := send(server, request)
				if response.Code != http.StatusForbidden {
					t.Errorf("%s answered %d, want %d, body %s", method, response.Code, http.StatusForbidden, response.Body)
				}
			}

			var lease v1alpha1.CapacityLease
			err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: refused}, &lease)
			if !apierrors.IsNotFound(err) {
				t.Errorf("reading the refused lease answered %v, want a not-found", err)
			}
		})
	}
}

// a proxied interface that was never told the origin it is reached at must refuse rather than guess one
func TestMutationsBehindAProxyWithoutAConfiguredOriginAreRefused(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "unconfigured-run"
	removeAfterTest(t, name)

	auth := completeAuthentication()
	options := writingOptions()
	options.Authentication = &auth
	server, err := New(options)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)

	response := send(server, newExternalMutation(t, http.MethodPost, leasesEndpoint, createRequestFixture(name)))
	if response.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d, body %s", response.Code, http.StatusForbidden, response.Body)
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

func overTheWire(t *testing.T, method, target, host string, body any) *http.Response {
	t.Helper()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the request body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, target, payload)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Host = host
	request.Header.Set(interfaceHeader, "true")
	request.Header.Set(fetchSiteHeader, sameOriginSite)
	request.Header.Set(originHeader, httpScheme+host)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// a name whose zone the caller owns can be pointed at this socket, and the browser will then call the request same-origin
func TestTheServedInterfaceAnchorsTheGuardToTheAddressItBound(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const addressed, rebound = "wire-run", "rebound-run"
	removeAfterTest(t, addressed)
	removeAfterTest(t, rebound)

	server, err := New(writingOptions())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	port := freePort(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- server.ListenAndServe(ctx, LoopbackAddress(port)) }()

	address := net.JoinHostPort(loopbackHost, strconv.Itoa(int(port)))
	shell := poll(t, httpScheme+address+"/")
	if err := shell.Body.Close(); err != nil {
		t.Errorf("close the response body: %v", err)
	}

	target := httpScheme + address + leasesEndpoint
	if created := overTheWire(t, http.MethodPost, target, address, createRequestFixture(addressed)); created.StatusCode != http.StatusCreated {
		t.Fatalf("a request addressed to %s answered %d, want %d", address, created.StatusCode, http.StatusCreated)
	}
	readLease(t, addressed)

	foreign := net.JoinHostPort("evil.example", strconv.Itoa(int(port)))
	if refused := overTheWire(t, http.MethodPost, target, foreign, createRequestFixture(rebound)); refused.StatusCode != http.StatusForbidden {
		t.Errorf("a request that reached the socket as %s answered %d, want %d", foreign, refused.StatusCode, http.StatusForbidden)
	}
	if err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: rebound}, &v1alpha1.CapacityLease{}); !apierrors.IsNotFound(err) {
		t.Errorf("reading the rebound lease answered %v, want a not-found", err)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(shutdownGrace * 2):
		t.Error("the interface did not stop once the context was cancelled")
	}
}

func TestAServerThatNeverBoundAnAddressRefusesEveryMutation(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server, err := New(writingOptions())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	for method, target := range map[string]string{
		http.MethodPost:   leasesEndpoint,
		http.MethodDelete: leaseEndpoint("unanchored-run"),
	} {
		response := send(server, newMutation(t, method, target, createRequestFixture("unanchored-run")))
		if response.Code != http.StatusForbidden {
			t.Errorf("%s answered %d, want %d, body %s", method, response.Code, http.StatusForbidden, response.Body)
		}
	}
}

func TestLeaseRoutesRefuseANameNoLeaseCouldCarry(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const traversal = "/api/leases/..%2Fvictim-run"
	server := newWritingServer(t)

	released := mutate(t, server, http.MethodDelete, traversal, nil)
	if released.Code != http.StatusBadRequest {
		t.Errorf("releasing answered %d, want %d, body %s", released.Code, http.StatusBadRequest, released.Body)
	}

	read := get(t, server, traversal)
	if read.Code != http.StatusBadRequest {
		t.Errorf("reading answered %d, want %d, body %s", read.Code, http.StatusBadRequest, read.Body)
	}
}

func TestLeaseCreateStoresANamedMachineType(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "named-type-run"
	removeAfterTest(t, name)

	request := createRequestFixture(name)
	request.Requirements = nil
	request.Size = "cx23"

	response := mutate(t, newWritingServer(t), http.MethodPost, leasesEndpoint, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusCreated, response.Body)
	}

	spec := readLease(t, name).Spec
	if spec.Size != "cx23" {
		t.Errorf("size = %q, want %q", spec.Size, "cx23")
	}
	if spec.Requirements != nil {
		t.Errorf("requirements = %+v, want none alongside a named type", spec.Requirements)
	}
}
