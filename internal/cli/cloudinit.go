package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lucawalz/horizon/internal/cloudinit"
	"github.com/lucawalz/horizon/internal/provider"
)

func NewCloudInitCmdForTest() *cobra.Command { return newCloudInitCmd() }

func newCloudInitCmd() *cobra.Command {
	opts := cloudinit.Options{}
	var files []string
	var installWatchdogUnit bool

	cmd := &cobra.Command{
		Use:          "cloud-init",
		Short:        "Render the cloud-init a burst node needs to join a cluster",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			parsed, err := parseFileFlags(files)
			if err != nil {
				return err
			}
			opts.Files = parsed
			opts.Labels = ensurePoolLabel(opts.Labels)
			opts.InstallWatchdogUnit = &installWatchdogUnit

			rendered, err := cloudinit.Render(opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), rendered)
			return err
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.Flavor, "flavor", "k3s",
		"Kubernetes distribution, one of "+strings.Join(cloudinit.Flavors(), ", "))
	flags.StringVar(&opts.Server, "server", "",
		"Control plane URL the node joins")
	flags.StringVar(&opts.Architecture, "arch", "",
		"Node CPU architecture, amd64 or arm64, defaulting to amd64")
	flags.StringSliceVar(&opts.Labels, "label", nil,
		"Node label, repeatable; the reserved pool label is always added")
	flags.StringSliceVar(&opts.Taints, "taint", nil,
		"Node taint, repeatable")
	flags.StringSliceVar(&files, "write-file", nil,
		"Extra file as path:permissions:content, repeatable")
	flags.StringSliceVar(&opts.PreCommands, "pre-command", nil,
		"Command to run before the join, repeatable")
	flags.StringSliceVar(&opts.PostCommands, "post-command", nil,
		"Command to run after the join, repeatable")
	flags.BoolVar(&installWatchdogUnit, "install-watchdog-unit", true,
		"Write and enable the self-destruct watchdog systemd unit")
	flags.StringVar(&opts.BinaryBaseURL, "binary-base-url", cloudinit.DefaultBinaryBaseURL,
		"Base URL the node downloads the horizon binary from")
	flags.BoolVar(&opts.Passthrough, "passthrough", false,
		"Emit no flavour content, leaving the caller to own the whole contract")

	return cmd
}

func ensurePoolLabel(labels []string) []string {
	poolLabel := provider.PoolLabelKey + "=" + provider.ReservedPoolValue
	for _, label := range labels {
		if label == poolLabel {
			return labels
		}
	}
	return append(labels, poolLabel)
}

func parseFileFlags(specs []string) ([]cloudinit.File, error) {
	files := make([]cloudinit.File, 0, len(specs))
	for _, spec := range specs {
		parts := strings.SplitN(spec, ":", 3)
		if len(parts) != 3 {
			return nil, errWriteFileFormat(spec)
		}
		files = append(files, cloudinit.File{Path: parts[0], Permissions: parts[1], Content: parts[2]})
	}
	return files, nil
}

func errWriteFileFormat(spec string) error {
	return fmt.Errorf("--write-file needs path:permissions:content, got %q", spec)
}
