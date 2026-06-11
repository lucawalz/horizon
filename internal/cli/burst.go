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
	"github.com/lucawalz/horizon/internal/zerotier"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

const (
	zerotierCleanupTimeout = 30 * time.Second
	migrateRollbackTimeout = 30 * time.Second
)

var burstSteps = []string{
	"Create Velero backup of target namespace",
	"Run terraform apply (provider: hetzner)",
	"Persist burst state file",
	"Authorize burst node in ZeroTier network",
	"Wait for node Ready",
	"Migrate workload to cloud node (label, affinity, evict)",
	"Wait for workload pods Running on cloud nodes",
}

type veleroClient interface {
	TriggerBackup(ctx context.Context, workloadNamespace, name string, poll, timeout time.Duration) error
}

type burstDeps struct {
	zt               zerotierAuthorizer
	prov             hetznerProvider
	kc               kubernetes.Interface
	vc               veleroClient
	skipPreflight    bool
	preExistingNodes map[string]bool
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
	token := config.Resolve(app.Config.ZeroTier.APITokenEnv, app.Config.ZeroTier.APIToken)
	if token == "" {
		return nil, fmt.Errorf("burst: zerotier api token env %q is empty", app.Config.ZeroTier.APITokenEnv)
	}
	zt := zerotier.NewClient("", token)
	prov, err := newBurstProvider(app, burstID)
	if err != nil {
		return nil, err
	}
	vc, err := velero.NewClient(app.Config.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("burst: velero client: %w", err)
	}
	return &burstDeps{zt: zt, prov: prov, kc: app.KubeClient, vc: vc}, nil
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

	if deps.preExistingNodes == nil {
		names, err := hetzner.ListNodeNames(ctx, deps.kc)
		if err != nil {
			return fmt.Errorf("burst: snapshot existing nodes: %w", err)
		}
		deps.preExistingNodes = names
	}

	var memberID string
	var authorized bool
	var burstNodeName string
	var savedMigrate *k8s.SavedState
	networkID := app.Config.ZeroTier.NetworkID

	r := &runner.Runner{}

	r.Add(runner.Step{
		Name: "velero-backup",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseBackingUp)
			name := fmt.Sprintf("horizon-burst-%s-%d", workload, time.Now().Unix())
			return deps.vc.TriggerBackup(ctx, workload, name, 5*time.Second, 10*time.Minute)
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
			if networkID == "" {
				return fmt.Errorf("terraform-apply: zerotier.network_id is empty in config")
			}
			deps.prov.SetRuntimeSecrets(networkID, sshPub, k3sURL, k3sToken)
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
				BurstID:          deps.prov.BurstID(),
				Hostname:         deps.prov.Hostname(),
				ZeroTierMemberID: deps.prov.ZeroTierMemberID(),
				HetznerServerID:  deps.prov.ServerID(),
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
		Name: "zerotier-auth",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseJoining)
			memberID = deps.prov.ZeroTierMemberID()
			if err := deps.zt.Authorize(ctx, networkID, memberID, deps.prov.Hostname()); err != nil {
				return fmt.Errorf("zerotier-auth: authorize: %w", err)
			}
			authorized = true
			return nil
		},
		Rollback: func(ctx context.Context) error {
			rbCtx, cancel := context.WithTimeout(ctx, zerotierCleanupTimeout)
			defer cancel()
			if memberID == "" {
				return nil
			}
			if authorized {
				_ = deps.zt.Deauthorize(rbCtx, networkID, memberID)
			}
			return deps.zt.DeleteMember(rbCtx, networkID, memberID)
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
		Name: "migrate-workload",
		Run: func(ctx context.Context) error {
			_ = k8s.WriteBurstPhase(ctx, deps.kc, deps.prov.BurstID(), k8s.BurstPhaseMigrating)
			state, err := k8s.Migrate(ctx, deps.kc, workload, burstNodeName)
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

func RunBurstForTest(ctx context.Context, app *App, zt zerotierAuthorizer, prov hetznerProvider, kc kubernetes.Interface, vc veleroClient, workload string) error {
	return runBurst(ctx, app, &burstDeps{
		zt: zt, prov: prov, kc: kc, vc: vc,
		skipPreflight:    true,
		preExistingNodes: map[string]bool{},
	}, workload)
}
