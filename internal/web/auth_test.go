package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testIssuer     = "https://issuer.example/realms/horizon"
	testAudience   = "horizon"
	testToken      = "a-token"
	credentialText = "eyJhbGciOiJSUzI1NiJ9.nobody-should-ever-log-this"
)

func completeAuthentication() Authentication {
	return Authentication{
		Issuer:        testIssuer,
		Audience:      testAudience,
		Header:        "Authorization",
		UsernameClaim: "preferred_username",
		GroupsClaim:   "groups",
	}
}

type stubVerifier struct {
	identity Identity
	err      error
}

func (s stubVerifier) VerifyToken(context.Context, string) (Identity, error) {
	return s.identity, s.err
}

func TestAuthenticationValidateNamesEverySettingItIsMissing(t *testing.T) {
	for name, testCase := range map[string]struct {
		strip func(*Authentication)
		named string
	}{
		"no issuer":         {func(a *Authentication) { a.Issuer = "" }, issuerSetting},
		"no audience":       {func(a *Authentication) { a.Audience = "" }, audienceSetting},
		"no header":         {func(a *Authentication) { a.Header = "" }, headerSetting},
		"no username claim": {func(a *Authentication) { a.UsernameClaim = "" }, usernameClaimSetting},
		"no groups claim":   {func(a *Authentication) { a.GroupsClaim = "" }, groupsClaimSetting},
	} {
		t.Run(name, func(t *testing.T) {
			auth := completeAuthentication()
			testCase.strip(&auth)

			err := auth.Validate()
			if err == nil {
				t.Fatalf("%s was accepted, want a rejection", name)
			}
			if !strings.Contains(err.Error(), testCase.named) {
				t.Errorf("error = %q, want it to name %s", err, testCase.named)
			}
		})
	}
}

func TestAuthenticationValidateAcceptsAnIssuerAndAudience(t *testing.T) {
	if err := completeAuthentication().Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

// the guard anchors to this value, so a value it cannot anchor to has to stop the process rather than pass through it
func TestAuthenticationValidateRefusesAnExternalOriginThatIsNotOne(t *testing.T) {
	for name, value := range map[string]string{
		"a bare name":        "horizon.example",
		"a scheme alone":     "https://",
		"a scheme-less pair": "//horizon.example",
		"a path":             "https://horizon.example/interface",
		"a query":            "https://horizon.example?view=leases",
		"a fragment":         "https://horizon.example#leases",
		"credentials":        "https://ada@horizon.example",
		"a foreign scheme":   "ftp://horizon.example",
		"an unreadable port": "https://horizon.example:port",
	} {
		t.Run(name, func(t *testing.T) {
			auth := completeAuthentication()
			auth.ExternalOrigin = value

			err := auth.Validate()
			if err == nil {
				t.Fatalf("%q was accepted, want a rejection", value)
			}
			if !strings.Contains(err.Error(), externalOriginSetting) {
				t.Errorf("error = %q, want it to name %s", err, externalOriginSetting)
			}
		})
	}
}

func TestAuthenticationValidateAcceptsAnAbsoluteExternalOrigin(t *testing.T) {
	for _, value := range []string{
		"https://horizon.example",
		"https://horizon.example/",
		"https://horizon.example:8443",
		"http://horizon.example",
	} {
		if err := completeAuthenticationOn(value).Validate(); err != nil {
			t.Errorf("%q was rejected with %v, want it accepted", value, err)
		}
	}
}

func TestNewRefusesAnExternalOriginThatIsNotOne(t *testing.T) {
	auth := completeAuthenticationOn("horizon.example")

	_, err := New(Options{
		Client:         failingReader{err: errors.New("unused")},
		Catalogue:      AbsentCatalogue(),
		Authentication: &auth,
	})
	if err == nil {
		t.Error("the server was built, want a rejection")
	}
}

func completeAuthenticationOn(origin string) Authentication {
	auth := completeAuthentication()
	auth.ExternalOrigin = origin
	return auth
}

func TestNewRefusesAnIncompleteAuthentication(t *testing.T) {
	auth := completeAuthentication()
	auth.Audience = ""

	_, err := New(Options{
		Client:         failingReader{err: errors.New("unused")},
		Catalogue:      AbsentCatalogue(),
		Authentication: &auth,
	})
	if err == nil {
		t.Error("the server was built, want a rejection")
	}
}

func newAuthenticatedTestServer(t *testing.T, auth Authentication) *Server {
	t.Helper()
	server, err := New(Options{
		Client:         failingReader{err: errors.New("unused")},
		Catalogue:      AbsentCatalogue(),
		Authentication: &auth,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)
	return server
}

func fetchMachines(server *Server, credential string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	if credential != "" {
		request.Header.Set("Authorization", credential)
	}
	recorder := httptest.NewRecorder()
	server.handler().ServeHTTP(recorder, request)
	return recorder
}

// an unwired seam must fail closed, so a build that never gains a verifier serves nothing rather than everything
func TestAuthenticatedInterfaceRejectsEveryRequestWithoutAVerifier(t *testing.T) {
	server := newAuthenticatedTestServer(t, completeAuthentication())

	response := fetchMachines(server, "Bearer "+testToken)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticatedInterfaceRejectsAnAbsentCredential(t *testing.T) {
	auth := completeAuthentication()
	auth.Verifier = stubVerifier{identity: Identity{Username: "ada"}}
	server := newAuthenticatedTestServer(t, auth)

	response := fetchMachines(server, "")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticatedInterfaceRejectsAnUnverifiedCredential(t *testing.T) {
	auth := completeAuthentication()
	auth.Verifier = stubVerifier{err: errors.New("the signature does not verify")}
	server := newAuthenticatedTestServer(t, auth)

	response := fetchMachines(server, "Bearer "+testToken)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func recordedLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var recorded bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &recorded
}

func TestAuthenticatedInterfaceRecordsWhyACredentialWasRejected(t *testing.T) {
	const reason = "the signature does not verify"
	recorded := recordedLog(t)
	auth := completeAuthentication()
	auth.Verifier = stubVerifier{err: errors.New(reason)}
	server := newAuthenticatedTestServer(t, auth)

	response := fetchMachines(server, "Bearer "+credentialText)

	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(response.Body.String(), credentialRejected) {
		t.Errorf("body = %q, want it to carry %q", response.Body.String(), credentialRejected)
	}
	if strings.Contains(response.Body.String(), reason) {
		t.Errorf("body = %q, want it to name no reason to an unauthenticated caller", response.Body.String())
	}
	if !strings.Contains(recorded.String(), reason) {
		t.Errorf("log = %q, want it to carry the reason verification failed", recorded.String())
	}
}

func TestAuthenticatedInterfaceRecordsNoPartOfTheRejectedCredential(t *testing.T) {
	recorded := recordedLog(t)
	auth := completeAuthentication()
	auth.Verifier = stubVerifier{err: errors.New("the signature does not verify")}
	server := newAuthenticatedTestServer(t, auth)

	fetchMachines(server, "Bearer "+credentialText)

	for _, part := range strings.Split(credentialText, ".") {
		if strings.Contains(recorded.String(), part) {
			t.Errorf("log = %q, want it to carry no part of the credential", recorded.String())
		}
	}
}

func TestAuthenticatedInterfaceCarriesTheVerifiedIdentity(t *testing.T) {
	verified := Identity{Username: "ada", Groups: []string{"platform"}}
	auth := completeAuthentication()
	auth.Verifier = stubVerifier{identity: verified}
	server := newAuthenticatedTestServer(t, auth)

	var carried Identity
	var present bool
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	server.authenticated(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		carried, present = IdentityFrom(r.Context())
	})).ServeHTTP(recorder, request)

	if !present {
		t.Fatal("the request carries no identity, want the verified one")
	}
	if carried.Username != verified.Username {
		t.Errorf("username = %q, want %q", carried.Username, verified.Username)
	}
}

// the dashboard supplies no authentication, so wrapping must stay off the path it serves
func TestUnauthenticatedInterfaceAnswersWithoutACredential(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := fetchMachines(server, "")
	if response.Code == http.StatusUnauthorized {
		t.Errorf("status = %d, want the interface to answer without a credential", response.Code)
	}
}
