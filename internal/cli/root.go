package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type App struct {
	Config     *config.Config
	KubeClient kubernetes.Interface
}

var app = &App{}

var rootCmd = &cobra.Command{
	Use:   "horizon",
	Short: "Homelab burst orchestrator",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		app.Config = cfg

		kc, err := k8s.NewClient(cfg.Kubeconfig)
		if err != nil {
			return fmt.Errorf("k8s client: %w", err)
		}
		app.KubeClient = kc

		if destructiveCmds[cmd.Name()] {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if err := RunPreFlight(ctx, app.Config, app.KubeClient, dryRun); err != nil {
				return err
			}
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().Bool("dry-run", false, "Print planned actions without executing")
	rootCmd.AddCommand(newStatusCmd(app))
	rootCmd.AddCommand(newBurstCmd(app))
	rootCmd.AddCommand(newUpCmd(app))
	rootCmd.AddCommand(newDownCmd(app))
	rootCmd.AddCommand(newDrainCmd(app))
	rootCmd.AddCommand(newWatchCmd(app))
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
