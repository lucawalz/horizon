package cli_test

import (
	"io"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

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
