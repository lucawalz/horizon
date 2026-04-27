package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lucawalz/horizon/internal/headscale"
	"github.com/lucawalz/horizon/internal/runner"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type downDeps struct {
	hs   headscaler
	prov hetznerProvider
	kc   kubernetes.Interface
}

var downSteps = []string{
	"Cordon node and evict non-DaemonSet pods",
	"Run terraform destroy (provider: hetzner)",
	"Delete burst node from Headscale",
	"Revoke Headscale pre-auth key",
	"Delete state file",
}

func newDownCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Tear down the burst node and revoke its Headscale state",
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
				return fmt.Errorf("down: read state: %w", err)
			}
			deps, err := newDownDeps(app)
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

func newDownDeps(app *App) (*downDeps, error) {
	apiKey := os.Getenv(app.Config.Headscale.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("down: headscale api key env %q is empty", app.Config.Headscale.APIKeyEnv)
	}
	hs := headscale.NewClient(app.Config.Headscale.APIURL, apiKey)
	return &downDeps{hs: hs, kc: app.KubeClient}, nil
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
		Name: "terraform-destroy",
		Run: func(ctx context.Context) error {
			return deps.prov.Destroy()
		},
	})

	r.Add(runner.Step{
		Name: "headscale-delete-node",
		Run: func(ctx context.Context) error {
			nodeID := st.HeadscaleNodeID
			if nodeID == "" {
				found, err := deps.hs.FindNodeByHostname(ctx, st.Hostname)
				if err != nil {
					return err
				}
				nodeID = found
			}
			if nodeID == "" {
				return nil
			}
			return deps.hs.DeleteNode(ctx, nodeID)
		},
	})

	r.Add(runner.Step{
		Name: "headscale-revoke-key",
		Run: func(ctx context.Context) error {
			if st.HeadscalePreAuthKey == "" {
				return nil
			}
			return deps.hs.RevokePreAuthKey(ctx, "burst-nodes", st.HeadscalePreAuthKey)
		},
	})

	r.Add(runner.Step{
		Name: "delete-state-file",
		Run: func(ctx context.Context) error {
			return DeleteState(stateDir, st.BurstID)
		},
	})

	return r.Run(ctx)
}

func cordonAndEvict(ctx context.Context, kc kubernetes.Interface, hostname string) error {
	if kc == nil {
		return nil
	}
	n, err := kc.CoreV1().Nodes().Get(ctx, hostname, metav1.GetOptions{})
	if err != nil {
		return nil
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
		if isDaemonSetPod(&pod) {
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

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func RunDownDryRunForTest(app *App) error {
	return runDownDryRun(app)
}

func RunDownForTest(ctx context.Context, app *App, hs headscaler, prov hetznerProvider, kc kubernetes.Interface, stateDir string, st BurstState) error {
	return runDown(ctx, app, &downDeps{hs: hs, prov: prov, kc: kc}, stateDir, st)
}

func ResolveBurstIDForTest(stateDir, flag string) (string, error) {
	return resolveBurstID(stateDir, flag)
}
