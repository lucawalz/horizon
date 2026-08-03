package cli

import (
	"time"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/internal/agent"
)

const (
	defaultTokenFile    = "/etc/horizon/token"
	defaultPollInterval = 15 * time.Second
	defaultMetadataURL  = "http://169.254.169.254/hetzner/v1/metadata"
	defaultStateDir     = "/run/horizon"
)

func NewWatchdogCmdForTest() *cobra.Command { return newWatchdogCmd() }

func newWatchdogCmd() *cobra.Command {
	var opts agent.Options

	cmd := &cobra.Command{
		Use:          "watchdog",
		Short:        "Enforce the node-side teardown deadline from the leased server itself",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			return agent.Run(ctrl.SetupSignalHandler(), opts)
		},
	}

	flags := cmd.Flags()
	flags.DurationVar(&opts.MaxLifetime, "max-lifetime", 0,
		"Age at which the server deletes itself, between 5m and 24h")
	flags.StringVar(&opts.TokenPath, "token-file", defaultTokenFile,
		"File holding the provider token used to delete this server")
	flags.StringVar(&opts.NodeName, "node-name", "",
		"Server to act on, defaulting to the hostname reported by the metadata service")
	flags.DurationVar(&opts.PollInterval, "poll-interval", defaultPollInterval,
		"Interval between deadline checks")
	flags.StringVar(&opts.MetadataBaseURL, "metadata-url", defaultMetadataURL,
		"Base URL of the instance metadata service")
	flags.StringVar(&opts.StateDir, "state-dir", defaultStateDir,
		"Directory holding the sentinel that records a teardown in progress")

	cobra.CheckErr(cmd.MarkFlagRequired("max-lifetime"))

	return cmd
}
