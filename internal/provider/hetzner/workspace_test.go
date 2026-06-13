package hetzner_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

var burstIDRegex = regexp.MustCompile(`^[a-f0-9]{4,16}$`)

func newWorkspaceTestConfig() *config.Config {
	return &config.Config{
		Hetzner: config.HetznerConfig{
			APITokenEnv: "HCLOUD_TOKEN",
			ServerType:  "cx22",
			Location:    "fsn1",
		},
		WireGuard: config.WireGuardConfig{
			HubHost:      "192.168.20.1",
			HubUser:      "root",
			HubPublicKey: "DPHflo9uj/HXikf/3LXERxRe/t7KOueakDX5dMAdm3Y=",
			Interface:    "wg0",
			ListenPort:   51820,
			Subnet:       "10.100.0.0/24",
			MasterIP:     "192.168.20.10",
		},
	}
}

func TestProvider_BurstIDDeterminesWorkspaceName(t *testing.T) {
	cfg := newWorkspaceTestConfig()
	p := hetzner.New(cfg, t.TempDir())

	id := p.BurstID()
	if !burstIDRegex.MatchString(id) {
		t.Errorf("BurstID %q does not match ^[a-f0-9]{4,16}$", id)
	}

	wantHostname := "horizon-burst-" + id
	if p.Hostname() != wantHostname {
		t.Errorf("Hostname: got %q, want %q", p.Hostname(), wantHostname)
	}

	wsName := "burst-" + id
	wsRegex := regexp.MustCompile(`^[a-zA-Z0-9-]{1,90}$`)
	if !wsRegex.MatchString(wsName) {
		t.Errorf("workspace name %q is not valid Terraform workspace syntax", wsName)
	}
	if len(wsName) > 90 {
		t.Errorf("workspace name %q exceeds 90 chars", wsName)
	}
}

func TestProvider_SetBurstIDForTest_InjectsKnownID(t *testing.T) {
	cfg := newWorkspaceTestConfig()
	p := hetzner.New(cfg, t.TempDir())

	p.SetBurstIDForTest("deadbeef")

	if p.BurstID() != "deadbeef" {
		t.Errorf("BurstID: got %q, want %q", p.BurstID(), "deadbeef")
	}
	if p.Hostname() != "horizon-burst-deadbeef" {
		t.Errorf("Hostname: got %q, want %q", p.Hostname(), "horizon-burst-deadbeef")
	}
}

func TestProvider_NewWithBurstID(t *testing.T) {
	cfg := newWorkspaceTestConfig()
	p, err := hetzner.NewWithBurstID(cfg, t.TempDir(), "a3f2")
	if err != nil {
		t.Fatalf("NewWithBurstID: %v", err)
	}

	if p.BurstID() != "a3f2" {
		t.Errorf("BurstID: got %q, want %q", p.BurstID(), "a3f2")
	}
	if p.Hostname() != "horizon-burst-a3f2" {
		t.Errorf("Hostname: got %q, want %q", p.Hostname(), "horizon-burst-a3f2")
	}
}

func TestProvider_NewWithBurstID_Rejects_InvalidID(t *testing.T) {
	cfg := newWorkspaceTestConfig()
	p, err := hetzner.NewWithBurstID(cfg, t.TempDir(), "INVALID!")
	if err == nil {
		t.Fatal("expected error for invalid burst_id")
	}
	if p != nil {
		t.Error("returned provider must be nil on error")
	}
	if !strings.Contains(err.Error(), "burst_id") {
		t.Errorf("error %q must contain \"burst_id\"", err.Error())
	}
}
