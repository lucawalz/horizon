package cli

import (
	"fmt"
	"os"

	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/tui"
	"github.com/lucawalz/horizon/internal/version"
	"github.com/spf13/cobra"
)

type App = core.App

var app = &App{}

var rootCmd = &cobra.Command{
	Use:     "horizon",
	Short:   "Homelab burst orchestrator",
	Version: version.Version(),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		contextName, err := cmd.Flags().GetString("context")
		if err != nil {
			return err
		}
		clusterName, err := cmd.Flags().GetString("cluster")
		if err != nil {
			return err
		}

		built, err := core.NewApp(contextName, clusterName)
		if err != nil {
			return err
		}
		*app = *built
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		contextName, err := cmd.Flags().GetString("context")
		if err != nil {
			return err
		}
		return tui.Run(app, contextName)
	},
}

func init() {
	rootCmd.PersistentFlags().String("context", "", "Kubeconfig context to target")
	rootCmd.PersistentFlags().String("cluster", "", "CAPI cluster name to target")
	rootCmd.AddCommand(newVersionCmd())
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
