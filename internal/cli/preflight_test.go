package cli_test

import (
	"context"
	"os"
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
