package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
			opts.InstallWatchdogUnit = &installWatchdogUnit

			if opts.Passthrough {
				if err := rejectGeneratedContentFlags(cmd.Flags()); err != nil {
					return err
				}
			} else {
				labels, err := ensurePoolLabel(opts.Labels)
				if err != nil {
					return err
				}
				opts.Labels = labels
			}

			rendered, err := cloudinit.Render(opts)
			if err != nil {
				return err
			}
			if !provider.HasPoolLabel(rendered) {
				return fmt.Errorf("the rendered cloud-init carries no %s node label, and the provider build refuses a cloud-init without it",
					provider.PoolLabelAssignment)
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
	flags.StringSliceVar(&opts.Labels, "label", nil,
		"Node label, repeatable; the reserved pool label is added automatically and conflicting values are rejected")
	flags.StringSliceVar(&opts.Taints, "taint", nil,
		"Node taint, repeatable")
	flags.StringArrayVar(&files, "write-file", nil,
		"Extra file as path:permissions:content, repeatable")
	flags.StringArrayVar(&opts.PreCommands, "pre-command", nil,
		"Command to run before the join, repeatable")
	flags.StringArrayVar(&opts.PostCommands, "post-command", nil,
		"Command to run after the join, repeatable")
	flags.BoolVar(&installWatchdogUnit, "install-watchdog-unit", true,
		"Write and enable the self-destruct watchdog systemd unit")
	flags.StringVar(&opts.BinaryBaseURL, "binary-base-url", cloudinit.DefaultBinaryBaseURL,
		"Base URL the node downloads the horizon binary from")
	flags.BoolVar(&opts.Passthrough, "passthrough", false,
		"Emit no flavour content, leaving the caller to own the whole contract")

	return cmd
}

var generatedContentFlags = []string{"flavor", "server", "label", "taint", "install-watchdog-unit", "binary-base-url"}

func rejectGeneratedContentFlags(flags *pflag.FlagSet) error {
	var discarded []string
	for _, name := range generatedContentFlags {
		if flags.Changed(name) {
			discarded = append(discarded, "--"+name)
		}
	}
	if len(discarded) == 0 {
		return nil
	}
	return fmt.Errorf("--passthrough emits no flavour or watchdog content, so %s would be discarded",
		strings.Join(discarded, ", "))
}

func ensurePoolLabel(labels []string) ([]string, error) {
	for _, label := range labels {
		key, value, _ := strings.Cut(label, "=")
		if key != provider.PoolLabelKey {
			continue
		}
		if value != provider.ReservedPoolValue {
			return nil, fmt.Errorf("label %s is set to %q, the burst node contract requires %q",
				provider.PoolLabelKey, value, provider.ReservedPoolValue)
		}
		return labels, nil
	}
	return append(labels, provider.PoolLabelAssignment), nil
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
