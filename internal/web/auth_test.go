package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testIssuer   = "https://issuer.example/realms/horizon"
	testAudience = "horizon"
	testToken    = "a-token"
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
