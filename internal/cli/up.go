package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lucawalz/horizon/internal/headscale"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
	"github.com/lucawalz/horizon/internal/runner"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type headscaler interface {
	CreatePreAuthKey(ctx context.Context, user string) (headscale.PreAuthKey, error)
	RevokePreAuthKey(ctx context.Context, user, key string) error
	FindNodeByHostname(ctx context.Context, hostname string) (string, error)
	DeleteNode(ctx context.Context, nodeID string) error
}

type hetznerProvider interface {
	SetRuntimeSecrets(preAuthKey, sshPublicKey, k3sURL, k3sToken string)
	GenerateTFVars() (map[string]string, error)
	Apply(ctx context.Context, vars map[string]string) error
	Destroy(ctx context.Context) error
	Hostname() string
	BurstID() string
	ServerID() string
}

type upDeps struct {
	hs            headscaler
	prov          hetznerProvider
	kc            kubernetes.Interface
	skipPreflight bool
}

var upSteps = []string{
	"Pre-flight checks",
	"Create Headscale pre-auth key (user: burst-nodes)",
	"Run terraform apply (provider: hetzner)",
	"Wait for node Ready and flannel pod Running",
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
	apiKey := os.Getenv(app.Config.Headscale.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("up: headscale api key env %q is empty", app.Config.Headscale.APIKeyEnv)
	}
	hs := headscale.NewClient(app.Config.Headscale.APIURL, apiKey)
	prov := hetzner.New(app.Config, app.Config.InfraPath)
	return &upDeps{hs: hs, prov: prov, kc: app.KubeClient}, nil
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

	var preAuthKey headscale.PreAuthKey

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
		Name: "headscale-preauth-key",
		Run: func(ctx context.Context) error {
			key, err := deps.hs.CreatePreAuthKey(ctx, "burst-nodes")
			if err != nil {
				return err
			}
			preAuthKey = key
			return nil
		},
		Rollback: func(ctx context.Context) error {
			if preAuthKey.Key == "" {
				return nil
			}
			return deps.hs.RevokePreAuthKey(ctx, "burst-nodes", preAuthKey.Key)
		},
	})

	r.Add(runner.Step{
		Name: "terraform-apply",
		Run: func(ctx context.Context) error {
			sshPub := os.Getenv("HORIZON_SSH_PUBLIC_KEY")
			k3sURL := os.Getenv("HORIZON_K3S_URL")
			k3sToken := os.Getenv("HORIZON_K3S_TOKEN")
			if sshPub == "" || k3sURL == "" || k3sToken == "" {
				return fmt.Errorf("terraform-apply: missing HORIZON_SSH_PUBLIC_KEY, HORIZON_K3S_URL, or HORIZON_K3S_TOKEN")
			}
			deps.prov.SetRuntimeSecrets(preAuthKey.Key, sshPub, k3sURL, k3sToken)
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
		Name: "wait-node-ready",
		Run: func(ctx context.Context) error {
			return hetzner.WaitNodeReady(ctx, deps.kc, deps.prov.Hostname(), 5*time.Minute, 5*time.Second)
		},
	})

	r.Add(runner.Step{
		Name: "write-state",
		Run: func(ctx context.Context) error {
			stateDir, err := stateDirOrTestOverride()
			if err != nil {
				return err
			}
			nodeID, err := deps.hs.FindNodeByHostname(ctx, deps.prov.Hostname())
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not resolve headscale node id for %s: %v\n", deps.prov.Hostname(), err)
				nodeID = ""
			}
			st := BurstState{
				BurstID:             deps.prov.BurstID(),
				Hostname:            deps.prov.Hostname(),
				HeadscaleNodeID:     nodeID,
				HeadscalePreAuthKey: preAuthKey.Key,
				HetznerServerID:     deps.prov.ServerID(),
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

func RunUpForTest(ctx context.Context, app *App, hs headscaler, prov hetznerProvider, kc kubernetes.Interface) error {
	return runUp(ctx, app, &upDeps{hs: hs, prov: prov, kc: kc, skipPreflight: true})
}

func SetStateDirForTest(dir string) (restore func()) {
	return setStateDirForTest(dir)
}
