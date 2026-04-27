package cli

import (
	"github.com/spf13/cobra"
)

func newUpCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Provision a burst node",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
