package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	servedUser        = "ada"
	servedGroup       = "platform"
	concurrentReaders = 64
)

var leaseResource = schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "capacityleases"}

func servedIdentity() Identity {
	return Identity{Username: servedUser, Groups: []string{servedGroup}}
}

func refusalFor(username string) error {
	return apierrors.NewForbidden(leaseResource, "", fmt.Errorf("user %q may not reach this resource", username))
}

type failingWriter struct{ err error }

func (f failingWriter) Create(context.Context, client.Object, ...client.CreateOption) error {
	return f.err
}

func (f failingWriter) Delete(context.Context, client.Object, ...client.DeleteOption) error {
	return f.err
}

func (f failingWriter) Extend(context.Context, string, time.Duration) error {
	return f.err
}

type failingConfigWriter struct{ err error }

func (f failingConfigWriter) Create(context.Context, *v1alpha1.ProviderConfig) error {
	return f.err
}

type identityClients struct {
	mu          sync.Mutex
	asked       []Identity
	failWith    error
	read        func(Identity) client.Reader
	write       func(Identity) LeaseWriter
	writeConfig func(Identity) ProviderConfigWriter
}

func (c *identityClients) record(identity Identity) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, identity)
	return c.failWith
}

func (c *identityClients) identities() []Identity {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.asked)
}

func (c *identityClients) ReaderFor(identity Identity) (client.Reader, error) {
	if err := c.record(identity); err != nil {
		return nil, err
	}
	return c.read(identity), nil
}

func (c *identityClients) WriterFor(identity Identity) (LeaseWriter, error) {
	if err := c.record(identity); err != nil {
		return nil, err
	}
	return c.write(identity), nil
}

func (c *identityClients) ConfigWriterFor(identity Identity) (ProviderConfigWriter, error) {
	if err := c.record(identity); err != nil {
		return nil, err
	}
	return c.writeConfig(identity), nil
}

func refusingClients() *identityClients {
	return &identityClients{
		read:  func(identity Identity) client.Reader { return failingReader{err: refusalFor(identity.Username)} },
		write: func(identity Identity) LeaseWriter { return failingWriter{err: refusalFor(identity.Username)} },
		writeConfig: func(identity Identity) ProviderConfigWriter {
			return failingConfigWriter{err: refusalFor(identity.Username)}
		},
	}
}

func newServedServer(t *testing.T, clients *identityClients) *Server {
	t.Helper()
	auth := completeAuthentication()
	auth.Verifier = tokenNamingVerifier{}
	server, err := New(Options{
		Catalogue:      AbsentCatalogue(),
		Authentication: &auth,
		Impersonation:  &Impersonation{Client: clients, Writer: clients, ConfigWriter: clients},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)
	return server
}

type tokenNamingVerifier struct{}

func (tokenNamingVerifier) VerifyToken(_ context.Context, token string) (Identity, error) {
	return Identity{Username: token, Groups: []string{token + "-group"}}, nil
}

func asIdentity(request *http.Request, identity Identity) *http.Request {
	return request.WithContext(withIdentity(request.Context(), identity))
}

func getAs(server *Server, identity Identity, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := asIdentity(httptest.NewRequest(http.MethodGet, target, nil), identity)
	server.routes().ServeHTTP(recorder, request)
	return recorder
}

// falling through to this process's own credentials would hand every unidentified caller the permissions the operator holds
func TestTheServedInterfaceRefusesAReadCarryingNoVerifiedIdentity(t *testing.T) {
	clients := refusingClients()
	server := newServedServer(t, clients)

	for _, target := range []string{leasesEndpoint, leaseEndpoint("unreachable"), machinesEndpoint, namespacesEndpoint} {
		t.Run(target, func(t *testing.T) {
			response := get(t, server, target)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
	if asked := clients.identities(); len(asked) != 0 {
		t.Errorf("the factory was asked for %v, want no client built at all", asked)
	}
}

func TestTheServedInterfaceRefusesAMutationCarryingNoVerifiedIdentity(t *testing.T) {
	clients := refusingClients()
	server := newServedServer(t, clients)

	for name, request := range map[string]*http.Request{
		"create":  newMutation(t, http.MethodPost, leasesEndpoint, createRequestFixture("unreachable")),
		"extend":  newMutation(t, http.MethodPatch, leaseEndpoint("unreachable"), extendRequestFixture(3*time.Hour)),
		"release": newMutation(t, http.MethodDelete, leaseEndpoint("unreachable"), nil),
		"configure": newMutation(t, http.MethodPost, providerConfigsEndpoint,
			configRequestFixture("unreachable")),
	} {
		t.Run(name, func(t *testing.T) {
			response := send(server, request)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
	if asked := clients.identities(); len(asked) != 0 {
		t.Errorf("the factory was asked for %v, want no client built at all", asked)
	}
}

func TestTheServedInterfaceReadsAsTheVerifiedIdentity(t *testing.T) {
	clients := refusingClients()
	server := newServedServer(t, clients)

	getAs(server, servedIdentity(), leasesEndpoint)

	asked := clients.identities()
	if len(asked) != 1 {
		t.Fatalf("the factory was asked %d times, want once", len(asked))
	}
	if asked[0].Username != servedUser {
		t.Errorf("the client was built for %q, want %q", asked[0].Username, servedUser)
	}
	if !slices.Equal(asked[0].Groups, []string{servedGroup}) {
		t.Errorf("the client was built with groups %v, want %v", asked[0].Groups, []string{servedGroup})
	}
}

func TestTheServedInterfaceMutatesAsTheVerifiedIdentity(t *testing.T) {
	clients := refusingClients()
	server := newServedServer(t, clients)

	send(server, asIdentity(newMutation(t, http.MethodPost, leasesEndpoint, createRequestFixture("named")), servedIdentity()))

	asked := clients.identities()
	if len(asked) != 1 {
		t.Fatalf("the factory was asked %d times, want once", len(asked))
	}
	if asked[0].Username != servedUser {
		t.Errorf("the writer was built for %q, want %q", asked[0].Username, servedUser)
	}
	if !slices.Equal(asked[0].Groups, []string{servedGroup}) {
		t.Errorf("the writer was built with groups %v, want %v", asked[0].Groups, []string{servedGroup})
	}
}

// two requests in flight must reach the cluster as their own callers, so no answer may name the other's user
func TestConcurrentRequestsNeverCrossIdentities(t *testing.T) {
	clients := refusingClients()
	server := newServedServer(t, clients)
	handler := server.handler()

	crossings := make(chan string, concurrentReaders)
	start := make(chan struct{})

	var inFlight sync.WaitGroup
	for i := range concurrentReaders {
		username := fmt.Sprintf("user-%d", i)
		inFlight.Add(1)
		go func() {
			defer inFlight.Done()
			<-start

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, leasesEndpoint, nil)
			request.Header.Set("Authorization", "Bearer "+username)
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusForbidden {
				crossings <- fmt.Sprintf("%s was answered %d, want %d", username, recorder.Code, http.StatusForbidden)
				return
			}

			var answer apiError
			if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
				crossings <- fmt.Sprintf("%s was answered %s, want a decodable body", username, recorder.Body)
				return
			}
			if named := fmt.Sprintf("%q", username); !strings.Contains(answer.Detail, named) {
				crossings <- fmt.Sprintf("%s was answered %q, want it to name %s", username, answer.Detail, named)
			}
		}()
	}
	close(start)
	inFlight.Wait()
	close(crossings)

	for crossing := range crossings {
		t.Error(crossing)
	}
	if asked := clients.identities(); len(asked) != concurrentReaders {
		t.Errorf("the factory was asked %d times, want %d", len(asked), concurrentReaders)
	}
}

// the adopter's RBAC decides, so a denial is an expected answer rather than a fault of this process
func TestAClusterRefusalAnswersAsAnAuthorisationFailureNamingTheUser(t *testing.T) {
	clients := refusingClients()
	server := newServedServer(t, clients)

	for name, response := range map[string]*httptest.ResponseRecorder{
		"list":      getAs(server, servedIdentity(), leasesEndpoint),
		"detail":    getAs(server, servedIdentity(), leaseEndpoint("named")),
		"machine":   getAs(server, servedIdentity(), machinesEndpoint),
		"namespace": getAs(server, servedIdentity(), namespacesEndpoint),
		"create": send(server, asIdentity(
			newMutation(t, http.MethodPost, leasesEndpoint, createRequestFixture("named")), servedIdentity(),
		)),
		"extend": send(server, asIdentity(
			newMutation(t, http.MethodPatch, leaseEndpoint("named"), extendRequestFixture(3*time.Hour)), servedIdentity(),
		)),
		"release": send(server, asIdentity(
			newMutation(t, http.MethodDelete, leaseEndpoint("named"), nil), servedIdentity(),
		)),
		"configure": send(server, asIdentity(
			newMutation(t, http.MethodPost, providerConfigsEndpoint, configRequestFixture("named")), servedIdentity(),
		)),
	} {
		t.Run(name, func(t *testing.T) {
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusForbidden, response.Body)
			}
			if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, servedUser) {
				t.Errorf("detail = %q, want it to name %s", detail, servedUser)
			}
		})
	}
}

func TestAnUnbuildableClientIsNotAnAuthorisationAnswer(t *testing.T) {
	clients := refusingClients()
	clients.failWith = errors.New("the cluster client could not be built")
	server := newServedServer(t, clients)

	response := getAs(server, servedIdentity(), leasesEndpoint)

	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func TestTheServedInterfaceWithoutAWriterFactoryRefusesEveryMutation(t *testing.T) {
	clients := refusingClients()
	auth := completeAuthentication()
	server, err := New(Options{
		Catalogue:      AbsentCatalogue(),
		Authentication: &auth,
		Impersonation:  &Impersonation{Client: clients},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)

	for name, request := range map[string]*http.Request{
		"create": newMutation(t, http.MethodPost, leasesEndpoint, createRequestFixture("named")),
		"configure": newMutation(t, http.MethodPost, providerConfigsEndpoint,
			configRequestFixture("named")),
	} {
		t.Run(name, func(t *testing.T) {
			response := send(server, asIdentity(request, servedIdentity()))

			if response.Code != http.StatusNotImplemented {
				t.Errorf("status = %d, want %d", response.Code, http.StatusNotImplemented)
			}
		})
	}
	if asked := clients.identities(); len(asked) != 0 {
		t.Errorf("the factory was asked for %v, want no writer built at all", asked)
	}
}

// only a verified identity can be impersonated, so the two settings stand or fall together
func TestNewRefusesImpersonationWithoutAuthentication(t *testing.T) {
	_, err := New(Options{
		Catalogue:     AbsentCatalogue(),
		Impersonation: &Impersonation{Client: refusingClients()},
	})
	if err == nil {
		t.Error("the server was built with impersonation and no authentication, want a rejection")
	}
}

func TestNewRefusesImpersonationWithoutAClientFactory(t *testing.T) {
	auth := completeAuthentication()

	_, err := New(Options{Catalogue: AbsentCatalogue(), Authentication: &auth, Impersonation: &Impersonation{}})
	if err == nil {
		t.Error("the server was built with an empty impersonation, want a rejection")
	}
}

func TestNewRefusesAConstructedClientBesideImpersonation(t *testing.T) {
	auth := completeAuthentication()

	for name, opts := range map[string]Options{
		"a reader": {
			Client:         failingReader{err: errors.New("unused")},
			Catalogue:      AbsentCatalogue(),
			Authentication: &auth,
			Impersonation:  &Impersonation{Client: refusingClients()},
		},
		"a writer": {
			Writer:         failingWriter{err: errors.New("unused")},
			Catalogue:      AbsentCatalogue(),
			Authentication: &auth,
			Impersonation:  &Impersonation{Client: refusingClients()},
		},
		"a provider config writer": {
			ConfigWriter:   failingConfigWriter{err: errors.New("unused")},
			Catalogue:      AbsentCatalogue(),
			Authentication: &auth,
			Impersonation:  &Impersonation{Client: refusingClients()},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(opts); err == nil {
				t.Error("the server was built, want a rejection")
			}
		})
	}
}

// the dashboard authenticates as the caller's own kubeconfig, so it answers with no identity in the request at all
func TestTheDashboardModeReadsWithNoIdentityAtAll(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())

	response := get(t, server, leasesEndpoint)

	if response.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
}
