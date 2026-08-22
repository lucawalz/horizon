package cli_test

import (
	"strconv"
	"testing"

	"github.com/spf13/pflag"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/manager"
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

// the dashboard is the one caller that supplies a writer, so a reader-only wiring would leave every create and release answering as read-only
func TestDashboardSuppliesAWriterAlongsideTheReader(t *testing.T) {
	opts := cli.DashboardOptionsForTest(clientfake.NewClientBuilder().WithScheme(manager.Scheme()).Build())

	if opts.Client == nil {
		t.Error("the dashboard supplies no cluster reader")
	}
	if opts.Writer == nil {
		t.Error("the dashboard supplies no writer, want the interface able to create and release leases")
	}
	if opts.Catalogue == nil {
		t.Error("the dashboard supplies no catalogue reader")
	}
}
