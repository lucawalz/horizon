package cli

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/prometheus"
	"github.com/lucawalz/horizon/internal/wireguard"
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
		hcloudEnv := cfg.Hetzner.APITokenEnv
		if hcloudEnv == "" {
			hcloudEnv = "HCLOUD_TOKEN"
		}
		if config.Resolve(hcloudEnv, cfg.Hetzner.APIToken) == "" {
			return fmt.Errorf("pre-flight: hetzner: HCLOUD_TOKEN environment variable is not set")
		}

		if cfg.WireGuard.HubHost == "" {
			return fmt.Errorf("pre-flight: wireguard: hub_host is empty in config")
		}
		if cfg.WireGuard.HubPublicKey == "" {
			return fmt.Errorf("pre-flight: wireguard: hub_public_key is empty in config")
		}

		k3sURL := config.Resolve(cfg.K3s.URLEnv, cfg.K3s.URL)
		if k3sURL == "" {
			return fmt.Errorf("pre-flight: k3s: K3S_URL is empty — set k3s.url or %s to the master DMZ IP", cfg.K3s.URLEnv)
		}
		if !urlHostMatches(k3sURL, cfg.WireGuard.MasterIP) {
			return fmt.Errorf("pre-flight: k3s: K3S_URL %s host must equal wireguard.master_ip %s", k3sURL, cfg.WireGuard.MasterIP)
		}

		if err := wireguard.HubReachable(ctx, cfg.WireGuard.HubHost, cfg.WireGuard.HubUser); err != nil {
			return fmt.Errorf("pre-flight: wireguard: %w", err)
		}
	}

	return nil
}

func urlHostMatches(rawURL, masterIP string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return masterIP != "" && u.Hostname() == masterIP
}
