package cli

import (
	"github.com/spf13/cobra"
)

func newDownCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down the burst node and revoke its Headscale state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
