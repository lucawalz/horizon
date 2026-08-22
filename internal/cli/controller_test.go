package cli_test

import (
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/manager"
)

func TestControllerCommandAcceptsTheChartFlags(t *testing.T) {
	cmd, _ := cli.NewControllerCmdForTest()

	for name, want := range map[string]string{
		"leader-elect":              "true",
		"metrics-bind-address":      ":8080",
		"health-probe-bind-address": ":8081",
		"lease-poll-interval":       "30s",
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

func TestControllerFlagsBindToTheOptionsTheManagerRuns(t *testing.T) {
	cmd, opts := cli.NewControllerCmdForTest()

	if opts.PollInterval != manager.DefaultPollInterval {
		t.Errorf("unparsed options poll every %s, want the default %s", opts.PollInterval, manager.DefaultPollInterval)
	}
	if err := cmd.ParseFlags([]string{"--lease-poll-interval=45s", "--metrics-bind-address=:9090"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if opts.PollInterval != 45*time.Second {
		t.Errorf("parsed options poll every %s, want 45s", opts.PollInterval)
	}
	if opts.MetricsAddress != ":9090" {
		t.Errorf("parsed options bind metrics to %q, want :9090", opts.MetricsAddress)
	}
}

func TestControllerCommandRefusesANegativePollInterval(t *testing.T) {
	cmd, _ := cli.NewControllerCmdForTest()
	cmd.SetArgs([]string{"--lease-poll-interval=-1s"})
	cmd.SilenceErrors = true

	if err := cmd.Execute(); err == nil {
		t.Error("a negative lease poll interval was accepted")
	}
}

func TestControllerCommandIsReachableFromRoot(t *testing.T) {
	for _, sub := range cli.NewRootCmdForTest().Commands() {
		if sub.Name() == "controller" {
			return
		}
	}
	t.Error("root command has no controller subcommand")
}
