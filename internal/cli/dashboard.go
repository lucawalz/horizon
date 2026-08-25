package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/internal/manager"
	"github.com/lucawalz/horizon/internal/web"
)

const defaultDashboardPort = 8973

func NewDashboardCmdForTest() *cobra.Command { return newDashboardCmd() }

func DashboardOptionsForTest(api client.Client) web.Options { return dashboardOptions(api) }

func newDashboardCmd() *cobra.Command {
	var port uint16

	cmd := &cobra.Command{
		Use:          "dashboard",
		Short:        "Serve the web interface on loopback",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDashboard(ctrl.SetupSignalHandler(), cmd.OutOrStdout(), port)
		},
	}

	cmd.Flags().Uint16Var(&port, "port", defaultDashboardPort,
		"Loopback port the interface listens on")

	return cmd
}

// the interface writes with the caller's own kubeconfig credentials, which is the whole of its authorisation
func dashboardOptions(api client.Client) web.Options {
	return web.Options{
		Client:       api,
		Writer:       web.LeaseWriterFor(api),
		ConfigWriter: web.ProviderConfigWriterFor(api),
		Catalogue:    web.AbsentCatalogue(),
	}
}

func runDashboard(ctx context.Context, out io.Writer, port uint16) error {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load the kubeconfig: %w", err)
	}
	api, err := client.New(restConfig, client.Options{Scheme: manager.Scheme()})
	if err != nil {
		return fmt.Errorf("build the cluster client: %w", err)
	}
	server, err := web.New(dashboardOptions(api))
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "serving the horizon dashboard on http://127.0.0.1:%d\n", port); err != nil {
		return err
	}
	return server.ListenAndServe(ctx, web.LoopbackAddress(port))
}
