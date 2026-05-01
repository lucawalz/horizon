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

func TestPreFlightZeroTierAPITokenMissing(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.ZeroTier.APITokenEnv = "ZEROTIER_API_TOKEN"
	cfg.ZeroTier.NetworkID = "abc123"
	cfg.K3s.URL = "https://10.147.20.1:6443"

	t.Setenv("ZEROTIER_API_TOKEN", "")
	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil {
		t.Fatal("expected error when ZEROTIER_API_TOKEN unset")
	}
	if !strings.Contains(err.Error(), "zerotier") || !strings.Contains(err.Error(), "ZEROTIER_API_TOKEN") {
		t.Errorf("error = %q, want contains 'zerotier' and 'ZEROTIER_API_TOKEN'", err.Error())
	}
}

func TestPreFlightZeroTierNetworkIDMissing(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.ZeroTier.APITokenEnv = "ZEROTIER_API_TOKEN"
	cfg.ZeroTier.NetworkID = ""
	cfg.K3s.URL = "https://10.147.20.1:6443"

	t.Setenv("ZEROTIER_API_TOKEN", "any")
	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil || !strings.Contains(err.Error(), "network_id") {
		t.Errorf("expected network_id error, got %v", err)
	}
}

func TestPreFlightK3sURLLAN(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.ZeroTier.APITokenEnv = "ZEROTIER_API_TOKEN"
	cfg.ZeroTier.NetworkID = "abc"
	cfg.K3s.URL = "https://192.168.2.191:6443"

	t.Setenv("ZEROTIER_API_TOKEN", "any")
	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil {
		t.Fatal("expected error for LAN K3S_URL")
	}
	want := "pre-flight: k3s: K3S_URL https://192.168.2.191:6443 is a LAN address — use the master's ZeroTier IP"
	if err.Error() != want {
		t.Errorf("got %q\nwant %q", err.Error(), want)
	}
}

func TestPreFlightK3sURLEmpty(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.ZeroTier.APITokenEnv = "ZEROTIER_API_TOKEN"
	cfg.ZeroTier.NetworkID = "abc"
	cfg.K3s.URL = ""
	cfg.K3s.URLEnv = "HORIZON_K3S_URL_TEST_UNSET"

	t.Setenv("ZEROTIER_API_TOKEN", "any")
	t.Setenv("HCLOUD_TOKEN", "dummy")
	t.Setenv("HORIZON_K3S_URL_TEST_UNSET", "")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil || !strings.Contains(err.Error(), "K3S_URL is empty") {
		t.Errorf("expected K3S_URL empty error, got %v", err)
	}
}

func TestPreFlightK3sURLZeroTierAccepted(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.ZeroTier.APITokenEnv = "ZEROTIER_API_TOKEN"
	cfg.ZeroTier.NetworkID = "abc"
	cfg.K3s.URL = "https://10.147.20.1:6443"

	t.Setenv("ZEROTIER_API_TOKEN", "any")
	t.Setenv("HCLOUD_TOKEN", "dummy")

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err != nil && strings.Contains(err.Error(), "K3S_URL") {
		t.Errorf("10.147.x.x must not be flagged as LAN: %v", err)
	}
}
