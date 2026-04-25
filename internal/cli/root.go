package cli

import (
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
		return nil
	},
}

func init() {
	rootCmd.AddCommand(newStatusCmd(app))
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
