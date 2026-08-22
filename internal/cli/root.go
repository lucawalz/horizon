package cli

import (
	"fmt"
	"os"

	"github.com/lucawalz/horizon/internal/version"
	"github.com/spf13/cobra"
)

var rootCmd = newRootCmd()

func NewRootCmdForTest() *cobra.Command { return newRootCmd() }

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "horizon",
		Short:   "Lease on-demand cloud capacity for a Kubernetes cluster",
		Version: version.Version(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	controllerCmd, _ := newControllerCmd()
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(controllerCmd)
	cmd.AddCommand(newDashboardCmd())
	cmd.AddCommand(newWatchdogCmd())
	cmd.AddCommand(newCloudInitCmd())
	return cmd
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
