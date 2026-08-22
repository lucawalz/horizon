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

func runDashboard(ctx context.Context, out io.Writer, port uint16) error {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load the kubeconfig: %w", err)
	}
	api, err := client.New(restConfig, client.Options{Scheme: manager.Scheme()})
	if err != nil {
		return fmt.Errorf("build the cluster client: %w", err)
	}
	// the interface writes with the caller's own kubeconfig credentials, which is the whole of its authorisation
	server, err := web.New(web.Options{Client: api, Writer: api, Catalogue: web.AbsentCatalogue()})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "serving the horizon dashboard on http://127.0.0.1:%d\n", port); err != nil {
		return err
	}
	return server.ListenAndServe(ctx, port)
}
