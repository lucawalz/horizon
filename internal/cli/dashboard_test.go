package cli_test

import (
	"strconv"
	"testing"

	"github.com/spf13/pflag"

	"github.com/lucawalz/horizon/internal/cli"
)

func TestDashboardCommandIsReachableFromRoot(t *testing.T) {
	for _, sub := range cli.NewRootCmdForTest().Commands() {
		if sub.Name() == "dashboard" {
			return
		}
	}
	t.Error("root command has no dashboard subcommand")
}

func TestDashboardCommandTakesAPortAlone(t *testing.T) {
	flags := cli.NewDashboardCmdForTest().Flags()

	port := flags.Lookup("port")
	if port == nil {
		t.Fatal("flag --port is not registered")
	}
	if _, err := strconv.ParseUint(port.DefValue, 10, 16); err != nil {
		t.Errorf("flag --port default = %q, want a port number", port.DefValue)
	}
}

func TestDashboardCommandExposesNoAddressFlag(t *testing.T) {
	flags := cli.NewDashboardCmdForTest().Flags()

	for _, name := range []string{"address", "bind", "bind-address", "host", "listen", "listen-address"} {
		if flags.Lookup(name) != nil {
			t.Errorf("flag --%s is registered, want the binding fixed to loopback", name)
		}
	}

	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Value.Type() == "string" {
			t.Errorf("flag --%s takes a string, want no flag able to carry an address", flag.Name)
		}
	})
}
