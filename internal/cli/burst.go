package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var burstSequence = []string{
	"Pre-flight checks (Velero API + Prometheus + terraform binary)",
	"Create Velero backup of target namespace",
	"Provision cloud node via Terraform (provider: %s)",
	"Join cloud node to K3s cluster via Tailscale",
	"Migrate workload to cloud node",
	"Monitor until workload is Running on cloud node",
}

func newBurstCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burst",
		Short: "Burst workload to cloud provider (Phase 1: dry-run only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun {
				if pf := cmd.Root().PersistentFlags().Lookup("dry-run"); pf != nil && pf.Value.String() == "true" {
					dryRun = true
				}
			}
			if dryRun {
				return runBurstDryRun(app)
			}
			return fmt.Errorf("burst: full burst not implemented; use --dry-run")
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print planned burst sequence without executing")
	return cmd
}

func runBurstDryRun(app *App) error {
	provider := "hetzner"
	if app.Config != nil && app.Config.Provider != "" {
		provider = app.Config.Provider
	}
	for i, step := range burstSequence {
		formatted := step
		if i == 2 {
			formatted = fmt.Sprintf(step, provider)
		}
		fmt.Printf("[dry-run] Step %d: %s\n", i+1, formatted)
	}
	fmt.Println("[dry-run] No actions executed.")
	return nil
}

func NewBurstCmdForTest(app *App) *cobra.Command {
	return newBurstCmd(app)
}
