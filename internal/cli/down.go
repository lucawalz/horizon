package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
	"github.com/lucawalz/horizon/internal/runner"
	"github.com/lucawalz/horizon/internal/zerotier"
	"github.com/spf13/cobra"
	policyv1 "k8s.io/api/policy/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type downDeps struct {
	zt   zerotierAuthorizer
	prov hetznerProvider
	kc   kubernetes.Interface
}

var downSteps = []string{
	"Cordon node and evict non-DaemonSet pods",
	"Remove burst node from ZeroTier network",
	"Run terraform destroy (provider: hetzner)",
	"Delete K3s node object from cluster",
	"Delete burst state file",
}

func newDownCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Tear down the burst node and revoke its ZeroTier membership",
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				return runDownDryRun(app)
			}
			burstID, _ := cmd.Flags().GetString("burst-id")
			stateDir, err := stateDirOrTestOverride()
			if err != nil {
				return fmt.Errorf("down: state dir: %w", err)
			}
			resolved, err := resolveBurstID(stateDir, burstID)
			if err != nil {
				return err
			}
			st, err := ReadState(stateDir, resolved)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("down: read state: %w", err)
				}
				st = BurstState{BurstID: resolved, Hostname: "horizon-burst-" + resolved}
			}
			deps, err := newDownDeps(app, resolved)
			if err != nil {
				return fmt.Errorf("down: init: %w", err)
			}
			return runDown(cmd.Context(), app, deps, stateDir, st)
		},
	}
	cmd.Flags().Bool("dry-run", false, "Print planned down sequence without executing")
	cmd.Flags().String("burst-id", "", "Burst id to tear down (omit when exactly one state file exists)")
	return cmd
}

func newDownDeps(app *App, burstID string) (*downDeps, error) {
	token := config.Resolve(app.Config.ZeroTier.APITokenEnv, app.Config.ZeroTier.APIToken)
	if token == "" {
		return nil, fmt.Errorf("down: zerotier api token env %q is empty", app.Config.ZeroTier.APITokenEnv)
	}
	zt := zerotier.NewClient("", token)
	prov, err := hetzner.NewWithBurstID(app.Config, app.Config.InfraPath, burstID)
	if err != nil {
		return nil, fmt.Errorf("down: provider: %w", err)
	}
	return &downDeps{zt: zt, prov: prov, kc: app.KubeClient}, nil
}

func resolveBurstID(stateDir, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	ids, err := ListStates(stateDir)
	if err != nil {
		return "", fmt.Errorf("down: list states: %w", err)
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("down: --burst-id required: no state files in %s", stateDir)
	}
	return "", fmt.Errorf("down: --burst-id required: multiple state files: %v", ids)
}

func runDownDryRun(app *App) error {
	for i, s := range downSteps {
		fmt.Printf("[dry-run] Step %d: %s\n", i+1, s)
	}
	fmt.Println("[dry-run] No actions executed.")
	return nil
}

func runDown(ctx context.Context, app *App, deps *downDeps, stateDir string, st BurstState) error {
	if ctx == nil {
		ctx = context.Background()
	}

	networkID := app.Config.ZeroTier.NetworkID
	r := &runner.Runner{}

	r.Add(runner.Step{
		Name: "cordon-and-evict",
		Run: func(ctx context.Context) error {
			c, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			return cordonAndEvict(c, deps.kc, st.Hostname)
		},
	})

	r.Add(runner.Step{
		Name: "zerotier-deauth",
		Run: func(ctx context.Context) error {
			if st.ZeroTierMemberID == "" {
				return nil
			}
			if networkID == "" {
				return fmt.Errorf("zerotier-deauth: zerotier.network_id is empty in config")
			}
			_ = deps.zt.Deauthorize(ctx, networkID, st.ZeroTierMemberID)
			return deps.zt.DeleteMember(ctx, networkID, st.ZeroTierMemberID)
		},
	})

	r.Add(runner.Step{
		Name: "terraform-destroy",
		Run: func(ctx context.Context) error {
			sshPub := config.Resolve(app.Config.K3s.SSHKeyEnv, app.Config.K3s.SSHPublicKey)
			k3sURL := config.Resolve(app.Config.K3s.URLEnv, app.Config.K3s.URL)
			k3sToken := config.Resolve(app.Config.K3s.TokenEnv, app.Config.K3s.Token)
			deps.prov.SetRuntimeSecrets(networkID, sshPub, k3sURL, k3sToken)
			return deps.prov.Destroy(ctx)
		},
	})

	r.Add(runner.Step{
		Name: "delete-k3s-node",
		Run: func(ctx context.Context) error {
			workload := burstWorkloadLabel(ctx, deps.kc, st.Hostname)
			if err := hetzner.DeleteNode(ctx, deps.kc, st.Hostname); err != nil {
				return err
			}
			if workload == "" {
				return nil
			}
			remaining, err := burstNodeCountForWorkload(ctx, deps.kc, workload)
			if err != nil {
				return err
			}
			if remaining > 0 {
				return nil
			}
			return k8s.ReconcileStrandedAffinity(ctx, deps.kc, workload)
		},
	})

	r.Add(runner.Step{
		Name: "delete-state-file",
		Run: func(ctx context.Context) error {
			if err := DeleteState(stateDir, st.BurstID); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		},
	})

	if err := r.Run(ctx); err != nil {
		return err
	}
	_ = k8s.ClearBurstPhase(ctx, deps.kc, st.BurstID)
	return nil
}

func cordonAndEvict(ctx context.Context, kc kubernetes.Interface, hostname string) error {
	if kc == nil {
		return nil
	}
	n, err := kc.CoreV1().Nodes().Get(ctx, hostname, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("cordon %s: %w", hostname, err)
	}
	if !n.Spec.Unschedulable {
		n.Spec.Unschedulable = true
		if _, err := kc.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("cordon %s: %w", hostname, err)
		}
	}
	pods, err := kc.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list pods on %s: %w", hostname, err)
	}
	for i := range pods.Items {
		pod := pods.Items[i]
		if pod.Spec.NodeName != hostname {
			continue
		}
		if k8s.IsDaemonSetPod(&pod) {
			continue
		}
		ev := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		if err := kc.CoreV1().Pods(pod.Namespace).EvictV1(ctx, ev); err != nil {
			return fmt.Errorf("evict %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func burstWorkloadLabel(ctx context.Context, kc kubernetes.Interface, hostname string) string {
	if kc == nil {
		return ""
	}
	n, err := kc.CoreV1().Nodes().Get(ctx, hostname, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	return n.Labels[k8s.NodeAffinityLabelKey]
}

func burstNodeCountForWorkload(ctx context.Context, kc kubernetes.Interface, workload string) (int, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("count burst nodes for %s: %w", workload, err)
	}
	count := 0
	for i := range nodes.Items {
		if nodes.Items[i].Labels[k8s.NodeAffinityLabelKey] == workload {
			count++
		}
	}
	return count, nil
}

func RunDownDryRunForTest(app *App) error {
	return runDownDryRun(app)
}

func RunDownForTest(ctx context.Context, app *App, zt zerotierAuthorizer, prov hetznerProvider, kc kubernetes.Interface, stateDir string, st BurstState) error {
	return runDown(ctx, app, &downDeps{zt: zt, prov: prov, kc: kc}, stateDir, st)
}

func ResolveBurstIDForTest(stateDir, flag string) (string, error) {
	return resolveBurstID(stateDir, flag)
}
