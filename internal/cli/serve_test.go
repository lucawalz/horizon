package cli_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/impersonate"
	"github.com/lucawalz/horizon/internal/manager"
	"github.com/lucawalz/horizon/internal/web"
)

const (
	servedTestAPIServer = "https://127.0.0.1:6443"
	servedTestAudience  = "horizon"
	servedTestBind      = "127.0.0.1:0"
	servedTestKeySize   = 2048
)

func servedTestAuthentication() web.Authentication {
	return web.Authentication{
		Issuer:        "https://issuer.example",
		Audience:      servedTestAudience,
		Header:        "Authorization",
		UsernameClaim: "preferred_username",
		GroupsClaim:   "groups",
	}
}

func TestServeCommandIsReachableFromRoot(t *testing.T) {
	for _, sub := range cli.NewRootCmdForTest().Commands() {
		if sub.Name() == "serve" {
			return
		}
	}
	t.Error("root command has no serve subcommand")
}

func TestServeCommandTakesTheAgreedFlags(t *testing.T) {
	cmd, _ := cli.NewServeCmdForTest()
	flags := cmd.Flags()

	for name, want := range map[string]string{
		"bind-address":    "0.0.0.0:8082",
		"auth-header":     "Authorization",
		"oidc-issuer":     "",
		"oidc-audience":   "",
		"username-claim":  "preferred_username",
		"groups-claim":    "groups",
		"external-origin": "",
	} {
		flag := flags.Lookup(name)
		if flag == nil {
			t.Errorf("flag --%s is not registered", name)
			continue
		}
		if flag.DefValue != want {
			t.Errorf("flag --%s default = %q, want %q", name, flag.DefValue, want)
		}
	}
}

// the key set belongs to the issuer that publishes it, so accepting a second source would let a token be signed by neither
func TestServeCommandTakesNoSeparateKeySetFlag(t *testing.T) {
	cmd, _ := cli.NewServeCmdForTest()

	if flag := cmd.Flags().Lookup("oidc-jwks-url"); flag != nil {
		t.Error("flag --oidc-jwks-url is registered, want the key set discovered from the issuer")
	}
}

// an unauthenticated privileged endpoint must never reach the point of binding, so the refusal happens before the listener exists
func TestServeCommandRefusesToStartWithoutTheOIDCSettings(t *testing.T) {
	for name, testCase := range map[string]struct {
		args  []string
		named string
	}{
		"nothing set":  {nil, "oidc-issuer"},
		"no issuer":    {[]string{"--oidc-audience=horizon"}, "oidc-issuer"},
		"no audience":  {[]string{"--oidc-issuer=https://issuer.example"}, "oidc-audience"},
		"empty issuer": {[]string{"--oidc-issuer=", "--oidc-audience=horizon"}, "oidc-issuer"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd, _ := cli.NewServeCmdForTest()
			cmd.SetArgs(testCase.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("serve started with %s, want a refusal", name)
			}
			if !strings.Contains(err.Error(), testCase.named) {
				t.Errorf("error = %q, want it to name %s", err, testCase.named)
			}
		})
	}
}

// the guard anchors to the configured origin, so an origin it cannot anchor to must never reach the point of binding
func TestServeCommandRefusesAnExternalOriginThatIsNotOne(t *testing.T) {
	cmd, _ := cli.NewServeCmdForTest()
	cmd.SetArgs([]string{
		"--oidc-issuer=https://issuer.example",
		"--oidc-audience=horizon",
		"--external-origin=horizon.example",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("serve started with a bare name as its external origin, want a refusal")
	}
	if !strings.Contains(err.Error(), "external-origin") {
		t.Errorf("error = %q, want it to name external-origin", err)
	}
}

func TestServeOptionsCarryTheOIDCSettingsIntoTheInterface(t *testing.T) {
	cmd, opts := cli.NewServeCmdForTest()
	if err := cmd.ParseFlags([]string{
		"--oidc-issuer=https://issuer.example",
		"--oidc-audience=horizon",
		"--external-origin=https://horizon.example",
		"--bind-address=127.0.0.1:9999",
	}); err != nil {
		t.Fatalf("parse the flags: %v", err)
	}

	if err := opts.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if opts.BindAddress != "127.0.0.1:9999" {
		t.Errorf("bind address = %q, want 127.0.0.1:9999", opts.BindAddress)
	}
	if opts.Authentication.Issuer != "https://issuer.example" {
		t.Errorf("issuer = %q, want https://issuer.example", opts.Authentication.Issuer)
	}
	if opts.Authentication.ExternalOrigin != "https://horizon.example" {
		t.Errorf("external origin = %q, want https://horizon.example", opts.Authentication.ExternalOrigin)
	}
}

func TestServeOptionsRefuseAnEmptyBindAddress(t *testing.T) {
	cmd, opts := cli.NewServeCmdForTest()
	if err := cmd.ParseFlags([]string{
		"--oidc-issuer=https://issuer.example",
		"--oidc-audience=horizon",
		"--bind-address=",
	}); err != nil {
		t.Fatalf("parse the flags: %v", err)
	}

	if err := opts.Validate(); err == nil {
		t.Error("an empty bind address was accepted, want a rejection")
	}
}

// the issuer is unreachable before anything is served, so the refusal must name what could not be reached
func TestServeRefusesAnUnreachableIssuer(t *testing.T) {
	const issuer = "http://127.0.0.1:1/realms/horizon"
	cmd, opts := cli.NewServeCmdForTest()
	if err := cmd.ParseFlags([]string{"--oidc-issuer=" + issuer, "--oidc-audience=horizon"}); err != nil {
		t.Fatalf("parse the flags: %v", err)
	}

	err := cli.RunServeForTest(t.Context(), io.Discard, *opts)
	if err == nil {
		t.Fatal("serve started against an unreachable issuer, want a refusal")
	}
	if !strings.Contains(err.Error(), issuer) {
		t.Errorf("error = %q, want it to name the issuer", err)
	}
}

// the served interface must lend a caller none of this process's own permissions, so it holds no client built at startup
func TestServeSuppliesImpersonatedClientsRatherThanItsOwn(t *testing.T) {
	clients, err := impersonate.New(&rest.Config{Host: servedTestAPIServer}, manager.Scheme())
	if err != nil {
		t.Fatalf("build the impersonated clients: %v", err)
	}

	opts := cli.ServeOptionsForTest(clients, servedTestAuthentication())

	if opts.Client != nil || opts.Writer != nil {
		t.Error("serve supplies a cluster client built at startup, want one built for each request")
	}
	if opts.Impersonation == nil || opts.Impersonation.Client == nil {
		t.Fatal("serve supplies no cluster client factory")
	}
	if opts.Impersonation.Writer == nil {
		t.Error("serve supplies no writer factory, want the interface able to create and release leases")
	}
	if _, err := web.New(opts); err != nil {
		t.Errorf("the interface refused the options serve builds: %v", err)
	}
}

type publishedIssuer struct {
	url  string
	keys *httptest.Server
}

func startPublishedIssuer(t *testing.T, keySet any) *publishedIssuer {
	t.Helper()
	published := &publishedIssuer{keys: httptest.NewServer(servedTestJSON(t, keySet))}
	t.Cleanup(published.keys.Close)

	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		servedTestJSON(t, map[string]any{
			"issuer":                                published.url,
			"jwks_uri":                              published.keys.URL,
			"authorization_endpoint":                published.url + "/auth",
			"token_endpoint":                        published.url + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})(w, nil)
	}))
	t.Cleanup(discovery.Close)
	published.url = discovery.URL
	return published
}

func servedTestJSON(t *testing.T, body any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("write a json response: %v", err)
		}
	}
}

func servedTestSigningKeySet(t *testing.T) map[string]any {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, servedTestKeySize)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	return map[string]any{"keys": []any{map[string]any{
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"kid": "horizon-serve",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}}
}

func servedTestSymmetricKeySet() map[string]any {
	return map[string]any{"keys": []any{map[string]any{
		"kty": "oct",
		"use": "sig",
		"kid": "horizon-shared-secret",
		"k":   base64.RawURLEncoding.EncodeToString([]byte("a-shared-secret-nobody-should-trust")),
	}}}
}

func runServeAgainst(t *testing.T, issuer string) error {
	t.Helper()
	t.Setenv("KUBECONFIG", writeTestKubeconfig(t))
	cmd, opts := cli.NewServeCmdForTest()
	if err := cmd.ParseFlags([]string{
		"--oidc-issuer=" + issuer,
		"--oidc-audience=" + servedTestAudience,
		"--bind-address=" + servedTestBind,
	}); err != nil {
		t.Fatalf("parse the flags: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	banner := &startupBanner{written: make(chan struct{})}
	served := make(chan error, 1)
	go func() { served <- cli.RunServeForTest(ctx, banner, *opts) }()

	select {
	case err := <-served:
		return err
	case <-banner.written:
		cancel()
		<-served
		return nil
	}
}

func refusalNaming(t *testing.T, err error, issuer, keySetURL string) {
	t.Helper()
	if err == nil {
		t.Fatal("serve started, want a refusal")
	}
	if !strings.Contains(err.Error(), issuer) {
		t.Errorf("error = %q, want it to name the issuer %s", err, issuer)
	}
	if !strings.Contains(err.Error(), keySetURL) {
		t.Errorf("error = %q, want it to name the key set %s", err, keySetURL)
	}
}

func TestServeRefusesAnIssuerWhoseKeySetIsUnreachable(t *testing.T) {
	published := startPublishedIssuer(t, map[string]any{"keys": []any{}})
	published.keys.Close()

	refusalNaming(t, runServeAgainst(t, published.url), published.url, published.keys.URL)
}

func TestServeRefusesAnIssuerPublishingAnEmptyKeySet(t *testing.T) {
	published := startPublishedIssuer(t, map[string]any{})

	refusalNaming(t, runServeAgainst(t, published.url), published.url, published.keys.URL)
}

func TestServeRefusesAnIssuerPublishingOnlyASymmetricKey(t *testing.T) {
	published := startPublishedIssuer(t, servedTestSymmetricKeySet())

	refusalNaming(t, runServeAgainst(t, published.url), published.url, published.keys.URL)
}

type startupBanner struct {
	text    strings.Builder
	written chan struct{}
	once    sync.Once
}

func (b *startupBanner) Write(p []byte) (int, error) {
	n, err := b.text.Write(p)
	b.once.Do(func() { close(b.written) })
	return n, err
}

func writeTestKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	body := "apiVersion: v1\nkind: Config\ncurrent-context: test\nclusters:\n- name: test\n  cluster:\n    server: " +
		servedTestAPIServer + "\n    insecure-skip-tls-verify: true\ncontexts:\n- name: test\n  context:\n" +
		"    cluster: test\n    user: test\nusers:\n- name: test\n  user:\n    token: unused\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write a kubeconfig: %v", err)
	}
	return path
}

func TestServeStartsWhenTheIssuerPublishesASigningKey(t *testing.T) {
	published := startPublishedIssuer(t, servedTestSigningKeySet(t))
	t.Setenv("KUBECONFIG", writeTestKubeconfig(t))

	cmd, opts := cli.NewServeCmdForTest()
	if err := cmd.ParseFlags([]string{
		"--oidc-issuer=" + published.url,
		"--oidc-audience=" + servedTestAudience,
		"--bind-address=" + servedTestBind,
	}); err != nil {
		t.Fatalf("parse the flags: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	banner := &startupBanner{written: make(chan struct{})}
	served := make(chan error, 1)
	go func() { served <- cli.RunServeForTest(ctx, banner, *opts) }()

	select {
	case <-banner.written:
	case err := <-served:
		cancel()
		t.Fatalf("serve returned before it bound: %v", err)
	}
	cancel()

	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(banner.text.String(), servedTestBind) {
		t.Errorf("output = %q, want it to name the bound address", banner.text.String())
	}
}
