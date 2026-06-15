package cli

import (
	"fmt"
	"os"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/version"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type App struct {
	Config        *config.Config
	KubeClient    kubernetes.Interface
	MetricsClient metricsclient.Interface
	CapiClient    *capi.Client
	Cluster       string
}

var app = &App{}

var rootCmd = &cobra.Command{
	Use:     "horizon",
	Short:   "Homelab burst orchestrator",
	Version: version.Version(),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		app.Config = cfg

		contextName, err := cmd.Flags().GetString("context")
		if err != nil {
			return err
		}
		clusterName, err := cmd.Flags().GetString("cluster")
		if err != nil {
			return err
		}

		kc, err := k8s.NewClientForContext(cfg.Kubeconfig, contextName)
		if err != nil {
			return fmt.Errorf("k8s client: %w", err)
		}
		app.KubeClient = kc

		mc, err := k8s.NewMetricsClient(cfg.Kubeconfig, contextName)
		if err != nil {
			return fmt.Errorf("metrics client: %w", err)
		}
		app.MetricsClient = mc

		cc, err := capi.NewClientForContext(cfg.Kubeconfig, contextName)
		if err != nil {
			return fmt.Errorf("capi client: %w", err)
		}
		app.CapiClient = cc

		if clusterName != "" {
			app.Cluster = clusterName
		} else {
			app.Cluster = cfg.Cluster
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().Bool("dry-run", false, "Print planned actions without executing")
	rootCmd.PersistentFlags().String("context", "", "Kubeconfig context to target")
	rootCmd.PersistentFlags().String("cluster", "", "CAPI cluster name to target")
	rootCmd.AddCommand(newStatusCmd(app))
	rootCmd.AddCommand(newBurstCmd(app))
	rootCmd.AddCommand(newUpCmd(app))
	rootCmd.AddCommand(newDownCmd(app))
	rootCmd.AddCommand(newDrainCmd(app))
	rootCmd.AddCommand(newBackupCmd(app))
	rootCmd.AddCommand(newRestoreCmd(app))
	rootCmd.AddCommand(newClusterCmd(app))
	rootCmd.AddCommand(newVersionCmd())
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
