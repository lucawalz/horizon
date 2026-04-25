package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/prometheus"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var destructiveCmds = map[string]bool{
	"burst": true,
	"up":    true,
	"down":  true,
	"watch": true,
}

func RunPreFlight(ctx context.Context, cfg *config.Config, clientset kubernetes.Interface, dryRun bool) error {
	if _, err := exec.LookPath("terraform"); err != nil {
		return fmt.Errorf("pre-flight: terraform binary: not found in PATH")
	}

	restCfg, err := clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("pre-flight: kubeconfig: %w", err)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("pre-flight: velero: discovery client: %w", err)
	}
	if _, err := dc.ServerResourcesForGroupVersion("velero.io/v1"); err != nil {
		return fmt.Errorf("pre-flight: velero: %w", err)
	}

	pc, err := prometheus.NewClient(clientset, cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("pre-flight: prometheus: %w", err)
	}
	defer pc.Close()
	if err := pc.IsHealthy(ctx); err != nil {
		return fmt.Errorf("pre-flight: prometheus: %w", err)
	}

	if !dryRun {
		if len(os.Getenv("HCLOUD_TOKEN")) == 0 {
			return fmt.Errorf("pre-flight: hetzner: HCLOUD_TOKEN environment variable is not set")
		}
	}

	return nil
}
