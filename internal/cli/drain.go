package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

const drainTimeout = 5 * time.Minute

func newDrainCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drain <node>",
		Short: "Cordon a node and evict non-DaemonSet pods, respecting PodDisruptionBudgets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDrain(cmd.Context(), app.KubeClient, args[0], drainTimeout)
		},
	}
	return cmd
}

func runDrain(ctx context.Context, kc kubernetes.Interface, nodeName string, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := k8s.Drain(ctx, kc, nodeName, timeout); err != nil {
		return fmt.Errorf("drain: %w", err)
	}
	fmt.Fprintf(os.Stdout, "0 non-DaemonSet pods remain on %s\n", nodeName)
	return nil
}

func RunDrainForTest(ctx context.Context, kc kubernetes.Interface, nodeName string) error {
	return runDrain(ctx, kc, nodeName, 100*time.Millisecond)
}
