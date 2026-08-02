package cli_test

import (
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

// The chart passes exactly these flags; a rename leaves the operator crash-looping on an unknown flag.
func TestControllerCommandAcceptsTheChartFlags(t *testing.T) {
	cmd := cli.NewControllerCmdForTest()

	for name, want := range map[string]string{
		"leader-elect":              "true",
		"metrics-bind-address":      ":8080",
		"health-probe-bind-address": ":8081",
		"ui-bind-address":           "",
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

func TestControllerCommandIsReachableFromRoot(t *testing.T) {
	for _, sub := range cli.NewRootCmdForTest().Commands() {
		if sub.Name() == "controller" {
			return
		}
	}
	t.Error("root command has no controller subcommand")
}
