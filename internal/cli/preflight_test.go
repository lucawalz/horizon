package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestPreFlightDryRunSkipsHeadscale(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Headscale.APIKeyEnv = "HEADSCALE_API_KEY"
	origKey := os.Getenv("HEADSCALE_API_KEY")
	os.Unsetenv("HEADSCALE_API_KEY")
	defer os.Setenv("HEADSCALE_API_KEY", origKey)

	err := cli.RunPreFlight(context.Background(), cfg, nil, true)
	if err != nil {
		if strings.Contains(err.Error(), "headscale") {
			t.Errorf("dry-run must not check headscale; got: %v", err)
		}
	}
}

func TestPreFlightHeadscaleAPIKeyMissing(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Headscale.APIKeyEnv = "HEADSCALE_API_KEY"

	origKey := os.Getenv("HEADSCALE_API_KEY")
	os.Unsetenv("HEADSCALE_API_KEY")
	defer os.Setenv("HEADSCALE_API_KEY", origKey)

	origToken := os.Getenv("HCLOUD_TOKEN")
	if origToken == "" {
		os.Setenv("HCLOUD_TOKEN", "dummy-token-for-test")
		defer os.Unsetenv("HCLOUD_TOKEN")
	}

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil {
		t.Fatal("expected error when HEADSCALE_API_KEY is not set, got nil")
	}
	want := "pre-flight: headscale: HEADSCALE_API_KEY is not set"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestPreFlightHeadscaleAPIUnreachable(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Headscale.APIKeyEnv = "HEADSCALE_API_KEY"
	cfg.Headscale.APIURL = "http://127.0.0.1:19999"

	origKey := os.Getenv("HEADSCALE_API_KEY")
	os.Setenv("HEADSCALE_API_KEY", "dummy-api-key")
	defer func() {
		if origKey == "" {
			os.Unsetenv("HEADSCALE_API_KEY")
		} else {
			os.Setenv("HEADSCALE_API_KEY", origKey)
		}
	}()

	origToken := os.Getenv("HCLOUD_TOKEN")
	if origToken == "" {
		os.Setenv("HCLOUD_TOKEN", "dummy-token-for-test")
		defer os.Unsetenv("HCLOUD_TOKEN")
	}

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil {
		t.Fatal("expected error when headscale API is unreachable, got nil")
	}
	if !strings.Contains(err.Error(), "pre-flight: headscale: API unreachable") {
		t.Errorf("got %q, want error containing %q", err.Error(), "pre-flight: headscale: API unreachable")
	}
}

func TestPreFlightHeadscaleUserMissing(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Headscale.APIKeyEnv = "HEADSCALE_API_KEY"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cfg.Headscale.APIURL = server.URL

	origKey := os.Getenv("HEADSCALE_API_KEY")
	os.Setenv("HEADSCALE_API_KEY", "dummy-api-key")
	defer func() {
		if origKey == "" {
			os.Unsetenv("HEADSCALE_API_KEY")
		} else {
			os.Setenv("HEADSCALE_API_KEY", origKey)
		}
	}()

	origToken := os.Getenv("HCLOUD_TOKEN")
	if origToken == "" {
		os.Setenv("HCLOUD_TOKEN", "dummy-token-for-test")
		defer os.Unsetenv("HCLOUD_TOKEN")
	}

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err == nil {
		t.Fatal("expected error when burst-nodes user is missing (404), got nil")
	}
	if !strings.Contains(err.Error(), "headscale") {
		t.Errorf("got %q, want error containing %q", err.Error(), "headscale")
	}
	if !strings.Contains(err.Error(), "burst-nodes") {
		t.Errorf("got %q, want error containing %q", err.Error(), "burst-nodes")
	}
}

func TestPreFlightHeadscaleOK(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Headscale.APIKeyEnv = "HEADSCALE_API_KEY"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"preAuthKeys":[]}`))
	}))
	defer server.Close()
	cfg.Headscale.APIURL = server.URL

	origKey := os.Getenv("HEADSCALE_API_KEY")
	os.Setenv("HEADSCALE_API_KEY", "dummy-api-key")
	defer func() {
		if origKey == "" {
			os.Unsetenv("HEADSCALE_API_KEY")
		} else {
			os.Setenv("HEADSCALE_API_KEY", origKey)
		}
	}()

	origToken := os.Getenv("HCLOUD_TOKEN")
	if origToken == "" {
		os.Setenv("HCLOUD_TOKEN", "dummy-token-for-test")
		defer os.Unsetenv("HCLOUD_TOKEN")
	}

	err := cli.RunPreFlight(context.Background(), cfg, nil, false)
	if err != nil && strings.Contains(err.Error(), "headscale") {
		t.Errorf("expected no headscale-prefixed error on 200 response, got %q", err.Error())
	}
}
