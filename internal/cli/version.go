package cli

import (
	"fmt"

	"github.com/lucawalz/horizon/internal/version"
	"github.com/spf13/cobra"
)

func NewVersionCmdForTest() *cobra.Command { return newVersionCmd() }

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.Version())
			return err
		},
	}
}
