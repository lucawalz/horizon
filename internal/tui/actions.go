package tui

import (
	"context"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/velero"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

const (
	actionTimeout = 15 * time.Minute
	drainTimeout  = 5 * time.Minute

	progressBuffer = 256

	infrastructureGroup     = "infrastructure.cluster.x-k8s.io"
	bootstrapGroup          = "bootstrap.cluster.x-k8s.io"
	defaultInfraKind        = "HCloudMachineTemplate"
	defaultClusterInfraKind = "HetznerCluster"
	defaultBootstrapKind    = "KThreesConfigTemplate"
	defaultPodCIDR          = "10.42.0.0/16"
	defaultServiceCIDR      = "10.43.0.0/16"
)

type progressMsg struct{ line string }

type actionDoneMsg struct {
	summary string
	err     error
}

type backupsLoadedMsg struct {
	backups []velerov1.Backup
	err     error
}

type restoresLoadedMsg struct {
	restores []velerov1.Restore
	err      error
}

type clusterChoice struct {
	name  string
	phase string
}

type clustersLoadedMsg struct {
	clusters []clusterChoice
	err      error
}

type manifestRenderedMsg struct {
	spec capi.ClusterSpec
	data []byte
	err  error
}

func newVeleroClient(app *core.App) (core.VeleroClient, error) {
	return velero.NewClient(app.Config.Kubeconfig)
}

func (m model) runScaleUp(target core.PoolTarget, nudge bool) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		err := core.ScaleUp(ctx, app.CapiClient, target, false, nudge, p)
		return "", err
	})
}

func (m model) runScaleDown(target core.PoolTarget, del bool) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		err := core.ScaleDown(ctx, app.CapiClient, target, false, del, p)
		return "", err
	})
}

func (m model) runNudge(target core.PoolTarget) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		if err := app.CapiClient.NudgeControlPlaneInitialized(ctx, target.Namespace, target.Cluster); err != nil {
			return "", err
		}
		p("Nudged control-plane-initialized for cluster " + target.Cluster + ".")
		return "", nil
	})
}

func (m model) runBurst(params core.BurstParams) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		err = core.Burst(ctx, app.CapiClient, app.KubeClient, vc, params, p)
		return "", err
	})
}

func (m model) runDrain(node string) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		if err := core.Drain(ctx, app.KubeClient, node, drainTimeout); err != nil {
			return "", err
		}
		return "0 non-DaemonSet pods remain on " + node, nil
	})
}

func (m model) runBackupCreate(spec velerov1.BackupSpec, name string, wait bool) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.CreateBackup(ctx, vc, spec, name, wait); err != nil {
			return "", err
		}
		return name, nil
	})
}

func (m model) runBackupDelete(name string) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.DeleteBackup(ctx, vc, name); err != nil {
			return "", err
		}
		return "delete request submitted; velero removes the backup and its snapshots in the background", nil
	})
}

func (m model) runRestoreCreate(spec velerov1.RestoreSpec, name string, wait bool) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.CreateRestore(ctx, vc, spec, name, wait); err != nil {
			return "", err
		}
		return name, nil
	})
}

func (m model) runClusterApply(spec capi.ClusterSpec) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		if err := core.ApplyCluster(ctx, app, spec); err != nil {
			return "", err
		}
		return "applied cluster " + spec.Namespace + "/" + spec.ClusterName, nil
	})
}

func (m model) runClusterWrite(spec capi.ClusterSpec) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		path, err := core.WriteClusterManifests(app, spec)
		if err != nil {
			return "", err
		}
		return "wrote manifests to " + path, nil
	})
}

func (m model) runClusterDelete(namespace, name string) tea.Cmd {
	app := m.app
	return streamCmd(func(ctx context.Context, p core.Progress) (string, error) {
		if err := core.DeleteCluster(ctx, app, namespace, name); err != nil {
			return "", err
		}
		return "deleted cluster " + namespace + "/" + name, nil
	})
}

func (m model) renderClusterPreview(spec capi.ClusterSpec) tea.Cmd {
	return func() tea.Msg {
		data, err := core.RenderCluster(spec)
		return manifestRenderedMsg{spec: spec, data: data, err: err}
	}
}

func (m model) loadBackups() tea.Cmd {
	app := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		vc, err := newVeleroClient(app)
		if err != nil {
			return backupsLoadedMsg{err: err}
		}
		backups, err := core.ListBackups(ctx, vc)
		if err != nil {
			return backupsLoadedMsg{err: err}
		}
		sort.Slice(backups, func(i, j int) bool {
			return backups[i].CreationTimestamp.After(backups[j].CreationTimestamp.Time)
		})
		return backupsLoadedMsg{backups: backups}
	}
}

func (m model) loadRestores() tea.Cmd {
	app := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		vc, err := newVeleroClient(app)
		if err != nil {
			return restoresLoadedMsg{err: err}
		}
		restores, err := core.ListRestores(ctx, vc)
		if err != nil {
			return restoresLoadedMsg{err: err}
		}
		return restoresLoadedMsg{restores: restores}
	}
}

func (m model) loadClusters() tea.Cmd {
	app := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		clusters, err := core.ListClusters(ctx, app, app.Config.Pools.Namespace)
		if err != nil {
			return clustersLoadedMsg{err: err}
		}
		rows := make([]clusterChoice, 0, len(clusters))
		for i := range clusters {
			rows = append(rows, clusterChoice{
				name:  clusters[i].Name,
				phase: core.ValueOrDash(clusters[i].Status.Phase),
			})
		}
		return clustersLoadedMsg{clusters: rows}
	}
}

func validateNamespace(ns string) error {
	return k8s.ValidateNamespace(ns)
}
