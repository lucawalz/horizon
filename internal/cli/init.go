package cli

import (
	"fmt"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/tui"
	"github.com/spf13/cobra"
)

func NewInitCmdForTest() *cobra.Command { return newInitCmd() }

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Run the first-run setup wizard",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			saved, err := tui.RunSetup()
			if err != nil {
				return err
			}
			if saved {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "configuration written to %s\n", config.DefaultConfigPath()); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
