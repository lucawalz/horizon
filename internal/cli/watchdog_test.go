package cli_test

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/lucawalz/horizon/internal/cli"
)

func runWatchdog(t *testing.T, args []string) error {
	t.Helper()
	cmd := cli.NewWatchdogCmdForTest()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestWatchdogCommandDefaultsMatchTheNodeImage(t *testing.T) {
	cmd := cli.NewWatchdogCmdForTest()

	for name, want := range map[string]string{
		"max-lifetime":  "0s",
		"token-file":    "/etc/horizon/token",
		"node-name":     "",
		"poll-interval": "15s",
		"metadata-url":  "http://169.254.169.254/hetzner/v1/metadata",
		"state-dir":     "/run/horizon",
	} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("flag --%s is not registered", name)
			continue
		}
		if flag.DefValue != want {
			t.Errorf("flag --%s default = %q, want %q", name, flag.DefValue, want)
		}
	}
}

func TestWatchdogCommandIsReachableFromRoot(t *testing.T) {
	for _, sub := range cli.NewRootCmdForTest().Commands() {
		if sub.Name() == "watchdog" {
			return
		}
	}
	t.Error("root command has no watchdog subcommand")
}

func TestWatchdogCommandRequiresAMaxLifetime(t *testing.T) {
	err := runWatchdog(t, []string{})
	if err == nil {
		t.Fatal("the watchdog must refuse to run without a max lifetime")
	}
	if !strings.Contains(err.Error(), "max-lifetime") {
		t.Errorf("error = %q, want it to name the missing flag", err)
	}
}

func TestWatchdogCommandRejectsAMaxLifetimeOutsideTheSchemaBounds(t *testing.T) {
	for _, value := range []string{"1s", "4m59s", "24h1m", "168h"} {
		if err := runWatchdog(t, []string{"--max-lifetime", value}); err == nil {
			t.Errorf("--max-lifetime %s was accepted, want it rejected", value)
		}
	}
}

func TestWatchdogCommandTakesTheTokenFromAFileOnly(t *testing.T) {
	var tokenFlags []string
	cli.NewWatchdogCmdForTest().Flags().VisitAll(func(flag *pflag.Flag) {
		if strings.Contains(flag.Name, "token") {
			tokenFlags = append(tokenFlags, flag.Name)
		}
	})

	if len(tokenFlags) != 1 || tokenFlags[0] != "token-file" {
		t.Errorf("token flags = %v, want only the file reference token-file", tokenFlags)
	}
}
