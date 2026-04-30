package cli

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/prometheus"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if cfg.Kubeconfig != "" {
		rules.ExplicitPath = cfg.Kubeconfig
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return fmt.Errorf("pre-flight: kubeconfig: %w", err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}
	dc, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("pre-flight: velero: discovery client: %w", err)
	}
	if _, err := dc.ServerResourcesForGroupVersion("velero.io/v1"); err != nil {
		return fmt.Errorf("pre-flight: velero: %w", err)
	}

	cs := clientset
	if cs == nil {
		cs, err = k8s.NewClient(cfg.Kubeconfig)
		if err != nil {
			return fmt.Errorf("pre-flight: k8s client: %w", err)
		}
	}
	pc, err := prometheus.NewClient(cs, cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("pre-flight: prometheus: %w", err)
	}
	defer pc.Close()
	if err := pc.IsHealthy(ctx); err != nil {
		return fmt.Errorf("pre-flight: prometheus: %w", err)
	}

	if !dryRun {
		if config.Resolve(cfg.Hetzner.APITokenEnv, cfg.Hetzner.APIToken) == "" {
			return fmt.Errorf("pre-flight: hetzner: HCLOUD_TOKEN environment variable is not set")
		}
		apiKey := config.Resolve(cfg.Headscale.APIKeyEnv, cfg.Headscale.APIKey)
		if apiKey == "" {
			return fmt.Errorf("pre-flight: headscale: %s is not set", cfg.Headscale.APIKeyEnv)
		}
		hsCtx, hsCancel := context.WithTimeout(ctx, 5*time.Second)
		defer hsCancel()
		req, err := http.NewRequestWithContext(hsCtx, http.MethodGet,
			cfg.Headscale.APIURL+"/api/v1/preauthkey?user=burst-nodes", nil)
		if err != nil {
			return fmt.Errorf("pre-flight: headscale: API unreachable: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("pre-flight: headscale: API unreachable: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("pre-flight: headscale: burst-nodes user not found")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("pre-flight: headscale: API unreachable (status %d)", resp.StatusCode)
		}
	}

	return nil
}
