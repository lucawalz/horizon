package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
	"github.com/lucawalz/horizon/internal/runner"
	"github.com/lucawalz/horizon/internal/velero"
	"github.com/lucawalz/horizon/internal/wireguard"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	wireguardCleanupTimeout = 30 * time.Second
	migrateRollbackTimeout  = 30 * time.Second
	peerKeepaliveSecs       = 25
)

var burstSteps = []string{
	"Create Velero backup of target namespace",
	"Run terraform apply (provider: hetzner)",
	"Persist burst state file",
	"Register burst node as WireGuard peer on hub",
	"Wait for node Ready",
	"Migrate workload to cloud node (label, affinity, evict)",
	"Wait for workload pods Running on cloud nodes",
}

type burstDeps struct {
	pm            wireguard.PeerManager
	prov          hetznerProvider
	kc            kubernetes.Interface
	vc            veleroClient
	skipPreflight bool
}

func newBurstCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burst",
		Short: "Burst workload to cloud provider",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun {
				if pf := cmd.Root().PersistentFlags().Lookup("dry-run"); pf != nil && pf.Value.String() == "true" {
					dryRun = true
				}
			}
			workload, _ := cmd.Flags().GetString("workload")
			if dryRun {
				return runBurstDryRun(app)
			}
			if workload == "" {
				return fmt.Errorf("burst: --workload is required")
			}
			if err := k8s.ValidateNamespace(workload); err != nil {
				return fmt.Errorf("burst: %w", err)
			}
			burstID, _ := cmd.Flags().GetString("burst-id")
			if burstID != "" && !burstIDPattern.MatchString(burstID) {
				return fmt.Errorf("burst: invalid burst_id %q", burstID)
			}
			deps, err := newBurstDeps(app, burstID)
			if err != nil {
				return fmt.Errorf("burst: init: %w", err)
			}
			return runBurst(cmd.Context(), app, deps, workload)
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print planned burst sequence without executing")
	cmd.Flags().String("workload", "", "target namespace to burst (required unless --dry-run)")
	cmd.Flags().String("burst-id", "", "Burst id for the node name, workspace, and state file (auto-generated when omitted)")
	return cmd
}

func newBurstDeps(app *App, burstID string) (*burstDeps, error) {
	wg := app.Config.WireGuard
	pm := wireguard.NewSSHPeerManager(wg.HubHost, wg.HubUser, wg.Interface, wg.ListenPort)
	prov, err := newBurstProvider(app, burstID)
	if err != nil {
		return nil, err
	}
	vc, err := velero.NewClient(app.Config.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("burst: velero client: %w", err)
	}
	return &burstDeps{pm: pm, prov: prov, kc: app.KubeClient, vc: vc}, nil
}

func newBurstProvider(app *App, burstID string) (hetznerProvider, error) {
	if burstID == "" {
		return hetzner.New(app.Config, app.Config.InfraPath), nil
	}
	prov, err := hetzner.NewWithBurstID(app.Config, app.Config.InfraPath, burstID)
	if err != nil {
		return nil, fmt.Errorf("burst: provider: %w", err)
	}
	return prov, nil
}

func runBurstDryRun(app *App) error {
	for i, s := range burstSteps {
		fmt.Printf("[dry-run] Step %d: %s\n", i+1, s)
	}
	fmt.Println("[dry-run] No actions executed.")
	return nil
}

func runBurst(parent context.Context, app *App, deps *burstDeps, workload string) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wgIP string
	var peerAdded bool
	var burstNodeName string
	var savedMigrate *k8s.SavedState

	r := &runner.Runner{}

	r.Add(runner.Step{
		Name: "velero-backup",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseBackingUp)
			name := fmt.Sprintf("horizon-burst-%s-%d", workload, time.Now().Unix())
			spec := velerov1.BackupSpec{IncludedNamespaces: []string{workload}, StorageLocation: "default"}
			return deps.vc.TriggerBackup(ctx, spec, name, 5*time.Second, 10*time.Minute)
		},
	})

	r.Add(runner.Step{
		Name: "terraform-apply",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseProvisioning)
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
		Name: "write-state",
		Run: func(ctx context.Context) error {
			stateDir, err := stateDirOrTestOverride()
			if err != nil {
				return err
			}
			st := BurstState{
				BurstID:         deps.prov.BurstID(),
				Hostname:        deps.prov.Hostname(),
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
		Rollback: func(ctx context.Context) error {
			stateDir, err := stateDirOrTestOverride()
			if err != nil {
				return err
			}
			return DeleteState(stateDir, deps.prov.BurstID())
		},
	})

	r.Add(runner.Step{
		Name: "wg-peer-add",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseJoining)
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
			burstNodeName = deps.prov.Hostname()
			return hetzner.WaitNodeReady(ctx, deps.kc, burstNodeName, 5*time.Minute, 5*time.Second)
		},
		Rollback: func(ctx context.Context) error {
			if burstNodeName == "" {
				return nil
			}
			return hetzner.DeleteNode(ctx, deps.kc, burstNodeName)
		},
	})

	r.Add(runner.Step{
		Name: "migrate-workload",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseMigrating)
			state, err := k8s.Migrate(ctx, deps.kc, workload, app.Config.Pools.Cluster)
			savedMigrate = state
			return err
		},
		Rollback: func(ctx context.Context) error {
			rbCtx, cancel := context.WithTimeout(ctx, migrateRollbackTimeout)
			defer cancel()
			_ = k8s.WriteBurstPhase(rbCtx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseTearingDown)
			return k8s.RollbackMigrate(rbCtx, deps.kc, savedMigrate)
		},
	})

	r.Add(runner.Step{
		Name: "wait-pods-running",
		Run: func(ctx context.Context) error {
			if err := k8s.WaitWorkloadOnBurstNodes(ctx, deps.kc, workload, 5*time.Second, 5*time.Minute); err != nil {
				return err
			}
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseRunning)
			return nil
		},
	})

	if err := r.Run(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = k8s.ClearBurstPhase(cleanupCtx, deps.kc, deps.prov.BurstID())
		return err
	}
	return nil
}

func NewBurstCmdForTest(app *App) *cobra.Command { return newBurstCmd(app) }

func NewBurstProviderBurstIDForTest(app *App, burstID string) (string, error) {
	prov, err := newBurstProvider(app, burstID)
	if err != nil {
		return "", err
	}
	return prov.BurstID(), nil
}

func RunBurstDryRunForTest(app *App) error { return runBurstDryRun(app) }

func RunBurstForTest(ctx context.Context, app *App, pm wireguard.PeerManager, prov hetznerProvider, kc kubernetes.Interface, vc veleroClient, workload string) error {
	return runBurst(ctx, app, &burstDeps{
		pm: pm, prov: prov, kc: kc, vc: vc,
		skipPreflight: true,
	}, workload)
}
