package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
	"github.com/lucawalz/horizon/internal/runner"
	"github.com/lucawalz/horizon/internal/wireguard"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type hetznerProvider interface {
	SetRuntimeSecrets(hubPublicKey, wgIP, sshPublicKey, k3sURL, k3sToken string)
	GenerateTFVars() (map[string]string, error)
	Apply(ctx context.Context, vars map[string]string) error
	Destroy(ctx context.Context) error
	Hostname() string
	BurstID() string
	ServerID() string
	ServerIP() string
	WireGuardPublicKey() string
	WireGuardIP() string
}

type upDeps struct {
	pm               wireguard.PeerManager
	prov             hetznerProvider
	kc               kubernetes.Interface
	skipPreflight    bool
	preExistingNodes map[string]bool
}

var upSteps = []string{
	"Pre-flight checks",
	"Run terraform apply (provider: hetzner)",
	"Register burst node as WireGuard peer on hub",
	"Wait for node Ready",
	"Persist burst state file (~/.local/state/horizon/<burst_id>.json)",
}

func newUpCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Provision a burst node on the configured cloud provider",
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				return runUpDryRun(app)
			}
			deps, err := newUpDeps(app)
			if err != nil {
				return fmt.Errorf("up: init: %w", err)
			}
			return runUp(cmd.Context(), app, deps)
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print planned up sequence without executing")
	return cmd
}

func newUpDeps(app *App) (*upDeps, error) {
	wg := app.Config.WireGuard
	pm := wireguard.NewSSHPeerManager(wg.HubHost, wg.HubUser, wg.Interface, wg.ListenPort)
	prov := hetzner.New(app.Config, app.Config.InfraPath)
	return &upDeps{pm: pm, prov: prov, kc: app.KubeClient}, nil
}

func runUpDryRun(app *App) error {
	for i, s := range upSteps {
		fmt.Printf("[dry-run] Step %d: %s\n", i+1, s)
	}
	fmt.Println("[dry-run] No actions executed.")
	return nil
}

func runUp(ctx context.Context, app *App, deps *upDeps) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if deps.preExistingNodes == nil {
		names, err := hetzner.ListNodeNames(ctx, deps.kc)
		if err != nil {
			return fmt.Errorf("up: snapshot existing nodes: %w", err)
		}
		deps.preExistingNodes = names
	}

	var wgIP string
	var peerAdded bool
	var burstNodeName string

	r := &runner.Runner{}

	r.Add(runner.Step{
		Name: "preflight",
		Run: func(ctx context.Context) error {
			if deps.skipPreflight {
				return nil
			}
			return RunPreFlight(ctx, app.Config, app.KubeClient, false)
		},
	})

	r.Add(runner.Step{
		Name: "terraform-apply",
		Run: func(ctx context.Context) error {
			sshPub := config.Resolve(app.Config.K3s.SSHKeyEnv, app.Config.K3s.SSHPublicKey)
			k3sURL := config.Resolve(app.Config.K3s.URLEnv, app.Config.K3s.URL)
			k3sToken := config.Resolve(app.Config.K3s.TokenEnv, app.Config.K3s.Token)
			if sshPub == "" || k3sURL == "" || k3sToken == "" {
				return fmt.Errorf("terraform-apply: missing %s, %s, or %s",
					app.Config.K3s.SSHKeyEnv, app.Config.K3s.URLEnv, app.Config.K3s.TokenEnv)
			}
			ip, err := wireguard.AllocateIP(app.Config.WireGuard.Subnet, deps.prov.BurstID())
			if err != nil {
				return fmt.Errorf("terraform-apply: %w", err)
			}
			wgIP = ip
			deps.prov.SetRuntimeSecrets(app.Config.WireGuard.HubPublicKey, wgIP, sshPub, k3sURL, k3sToken)
			vars, err := deps.prov.GenerateTFVars()
			if err != nil {
				return err
			}
			return deps.prov.Apply(ctx, vars)
		},
		Rollback: func(ctx context.Context) error {
			return deps.prov.Destroy(ctx)
		},
	})

	r.Add(runner.Step{
		Name: "wg-peer-add",
		Run: func(ctx context.Context) error {
			if err := deps.pm.AddPeer(ctx, wireguard.Peer{
				PublicKey:     deps.prov.WireGuardPublicKey(),
				Endpoint:      deps.prov.ServerIP(),
				AllowedIP:     wgIP + "/32",
				KeepaliveSecs: peerKeepaliveSecs,
			}); err != nil {
				return fmt.Errorf("wg-peer-add: %w", err)
			}
			peerAdded = true
			return nil
		},
		Rollback: func(ctx context.Context) error {
			rbCtx, cancel := context.WithTimeout(ctx, wireguardCleanupTimeout)
			defer cancel()
			if !peerAdded {
				return nil
			}
			return deps.pm.RemovePeer(rbCtx, deps.prov.WireGuardPublicKey())
		},
	})

	r.Add(runner.Step{
		Name: "wait-node-ready",
		Run: func(ctx context.Context) error {
			name, err := hetzner.WaitNewNodeReady(ctx, deps.kc, deps.preExistingNodes, 5*time.Minute, 5*time.Second)
			burstNodeName = name
			return err
		},
		Rollback: func(ctx context.Context) error {
			if burstNodeName == "" {
				return nil
			}
			return hetzner.DeleteNode(ctx, deps.kc, burstNodeName)
		},
	})

	r.Add(runner.Step{
		Name: "write-state",
		Run: func(ctx context.Context) error {
			stateDir, err := stateDirOrTestOverride()
			if err != nil {
				return err
			}
			st := BurstState{
				BurstID:         deps.prov.BurstID(),
				Hostname:        burstNodeName,
				WireGuardIP:     wgIP,
				WireGuardPubKey: deps.prov.WireGuardPublicKey(),
				HetznerServerID: deps.prov.ServerID(),
			}
			if err := WriteState(stateDir, st); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "burst_id: %s\n", st.BurstID)
			return nil
		},
	})

	return r.Run(ctx)
}

var testStateDirOverride string

func setStateDirForTest(dir string) (restore func()) {
	prev := testStateDirOverride
	testStateDirOverride = dir
	return func() { testStateDirOverride = prev }
}

func stateDirOrTestOverride() (string, error) {
	if testStateDirOverride != "" {
		return testStateDirOverride, nil
	}
	return DefaultStateDir()
}

func RunUpDryRunForTest(app *App) error {
	return runUpDryRun(app)
}

func RunUpForTest(ctx context.Context, app *App, pm wireguard.PeerManager, prov hetznerProvider, kc kubernetes.Interface) error {
	return runUp(ctx, app, &upDeps{pm: pm, prov: prov, kc: kc, skipPreflight: true, preExistingNodes: map[string]bool{}})
}

func SetStateDirForTest(dir string) (restore func()) {
	return setStateDirForTest(dir)
}
