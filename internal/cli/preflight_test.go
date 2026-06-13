package cli_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
)

func minimalConfig(t *testing.T) *config.Config {
	t.Helper()
	home, _ := os.UserHomeDir()
	kubecfg := home + "/.kube/config"
	if _, err := os.Stat(kubecfg); err != nil {
		t.Skip("~/.kube/config not present — skipping live cluster test")
	}
	return &config.Config{
		Kubeconfig: kubecfg,
		Thresholds: config.ThresholdConfig{Burst: 0.80},
	}
}

func TestPreFlightDryRunSkipsCredentials(t *testing.T) {
	cfg := minimalConfig(t)
	origToken := os.Getenv("HCLOUD_TOKEN")
	os.Unsetenv("HCLOUD_TOKEN")
	defer os.Setenv("HCLOUD_TOKEN", origToken)

	err := cli.RunPreFlight(context.Background(), cfg, nil, true)
	if err != nil {
		if err.Error() == "pre-flight: hetzner: HCLOUD_TOKEN environment variable is not set" {
			t.Errorf("dry-run pre-flight must not check HCLOUD_TOKEN; got: %v", err)
		}
	}
}

func TestPreFlightWireGuardHubHostMissing(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.WireGuard.HubHost = ""
	cfg.WireGuard.HubPublicKey = "hubkey"
	cfg.WireGuard.MasterIP = "10.20.0.10"
	cfg.K3s.URL = "https://10.20.0.10:6443"

	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil || !strings.Contains(err.Error(), "hub_host") {
		t.Errorf("expected hub_host error, got %v", err)
	}
}

func TestPreFlightWireGuardHubPublicKeyMissing(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.WireGuard.HubHost = "10.20.0.1"
	cfg.WireGuard.HubPublicKey = ""
	cfg.WireGuard.MasterIP = "10.20.0.10"
	cfg.K3s.URL = "https://10.20.0.10:6443"

	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil || !strings.Contains(err.Error(), "hub_public_key") {
		t.Errorf("expected hub_public_key error, got %v", err)
	}
}

func TestPreFlightK3sURLStaleRejected(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.WireGuard.HubHost = "10.20.0.1"
	cfg.WireGuard.HubPublicKey = "hubkey"
	cfg.WireGuard.MasterIP = "10.20.0.10"
	cfg.K3s.URL = "https://10.147.17.161:6443"

	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil {
		t.Fatal("expected error for stale K3S_URL not matching master_ip")
	}
	if !strings.Contains(err.Error(), "master_ip") {
		t.Errorf("error %q must mention master_ip", err.Error())
	}
}

func TestPreFlightK3sURLEmpty(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.WireGuard.HubHost = "10.20.0.1"
	cfg.WireGuard.HubPublicKey = "hubkey"
	cfg.WireGuard.MasterIP = "10.20.0.10"
	cfg.K3s.URL = ""
	cfg.K3s.URLEnv = "HORIZON_K3S_URL_TEST_UNSET"

	t.Setenv("HCLOUD_TOKEN", "dummy")
	t.Setenv("HORIZON_K3S_URL_TEST_UNSET", "")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil || !strings.Contains(err.Error(), "K3S_URL is empty") {
		t.Errorf("expected K3S_URL empty error, got %v", err)
	}
}

func TestPreFlightK3sURLDMZMasterAccepted(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.WireGuard.HubHost = "10.20.0.1"
	cfg.WireGuard.HubPublicKey = "hubkey"
	cfg.WireGuard.MasterIP = "10.20.0.10"
	cfg.K3s.URL = "https://10.20.0.10:6443"

	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err != nil && strings.Contains(err.Error(), "master_ip") {
		t.Errorf("DMZ master IP matching master_ip must be accepted: %v", err)
	}
}
