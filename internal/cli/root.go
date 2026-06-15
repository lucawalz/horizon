package cli

import (
	"fmt"
	"os"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type App struct {
	Config     *config.Config
	KubeClient kubernetes.Interface
	CapiClient *capi.Client
	Cluster    string
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

		cc, err := capi.NewClient(cfg.Kubeconfig)
		if err != nil {
			return fmt.Errorf("capi client: %w", err)
		}
		app.CapiClient = cc
		app.Cluster = cfg.Cluster

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
	rootCmd.AddCommand(newBackupCmd(app))
	rootCmd.AddCommand(newRestoreCmd(app))
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
