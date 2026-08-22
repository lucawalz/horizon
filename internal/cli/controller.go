package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/internal/manager"
)

const (
	defaultMetricsAddress = ":8080"
	defaultHealthAddress  = ":8081"
)

func NewControllerCmdForTest() (*cobra.Command, *manager.Options) { return newControllerCmd() }

func newControllerCmd() (*cobra.Command, *manager.Options) {
	opts := &manager.Options{}

	cmd := &cobra.Command{
		Use:          "controller",
		Short:        "Run the in-cluster capacity lease controller",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		PreRunE: func(*cobra.Command, []string) error {
			if opts.PollInterval < 0 {
				return fmt.Errorf("--lease-poll-interval must not be negative, got %s", opts.PollInterval)
			}
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			return manager.Run(ctrl.SetupSignalHandler(), *opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.LeaderElection, "leader-elect", true,
		"Hold a leader election lease so only one replica reconciles")
	flags.StringVar(&opts.MetricsAddress, "metrics-bind-address", defaultMetricsAddress,
		"Address the metrics endpoint binds to")
	flags.StringVar(&opts.HealthAddress, "health-probe-bind-address", defaultHealthAddress,
		"Address the health and readiness endpoints bind to")
	flags.DurationVar(&opts.PollInterval, "lease-poll-interval", manager.DefaultPollInterval,
		"Fallback interval between lease reconciles, used for whatever the node watch misses")

	return cmd, opts
}
