package cli_test

import (
	"io"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/impersonate"
	"github.com/lucawalz/horizon/internal/manager"
	"github.com/lucawalz/horizon/internal/web"
)

const servedTestAPIServer = "https://127.0.0.1:6443"

func servedTestAuthentication() web.Authentication {
	return web.Authentication{
		Issuer:        "https://issuer.example",
		Audience:      "horizon",
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
