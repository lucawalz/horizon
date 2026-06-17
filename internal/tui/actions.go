package tui

import (
	"context"
	"io"
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
)

type backupsLoadedMsg struct {
	backups []velerov1.Backup
	err     error
}

type restoresLoadedMsg struct {
	restores []velerov1.Restore
	err      error
}

type schedulesLoadedMsg struct {
	schedules []velerov1.Schedule
	err       error
}

type storageLocationsLoadedMsg struct {
	locations []velerov1.BackupStorageLocation
	err       error
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
	data []byte
	err  error
}

type flavorRequest struct {
	name     string
	template []byte
	vars     map[string]string
}

func newVeleroClient(app *core.App) (core.VeleroClient, error) {
	return velero.NewClient(app.Config.Kubeconfig)
}

func (m model) runScaleUp(target core.PoolTarget) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		err := core.ScaleUp(ctx, app.CapiClient, target, false, p)
		return "", err
	})
}

func (m model) runScaleDown(target core.PoolTarget, del bool) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		err := core.ScaleDown(ctx, app.CapiClient, target, false, del, p)
		return "", err
	})
}

func (m model) runBurst(params core.BurstParams) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
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
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		var out io.Writer
		if m.debug {
			out = &lineWriter{sink: p.Debug}
		}
		if err := core.Drain(ctx, app.KubeClient, node, drainTimeout, out); err != nil {
			return "", err
		}
		return "0 non-DaemonSet pods remain on " + node, nil
	})
}

func (m model) runBackupCreate(spec velerov1.BackupSpec, name string, wait bool) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.CreateBackup(ctx, vc, spec, name, wait, p); err != nil {
			return "", err
		}
		return name, nil
	})
}

func (m model) runBackupDelete(name string) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.DeleteBackup(ctx, vc, name, p); err != nil {
			return "", err
		}
		return "delete request submitted; velero removes the backup and its snapshots in the background", nil
	})
}

func (m model) runRestoreCreate(spec velerov1.RestoreSpec, name string, wait bool) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.CreateRestore(ctx, vc, spec, name, wait, p); err != nil {
			return "", err
		}
		return name, nil
	})
}

func (m model) runScheduleCreate(spec velerov1.ScheduleSpec, name string) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.CreateSchedule(ctx, vc, spec, name, p); err != nil {
			return "", err
		}
		return name, nil
	})
}

func (m model) runScheduleDelete(name string) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.DeleteSchedule(ctx, vc, name, p); err != nil {
			return "", err
		}
		return "deleted schedule " + name, nil
	})
}

func (m model) runBSLCreate(spec velerov1.BackupStorageLocationSpec, name string) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		if err := core.CreateBackupStorageLocation(ctx, vc, spec, name, p); err != nil {
			return "", err
		}
		return "created storage location " + name + "; this registers the CR only, the bucket must already exist via the operator's IaC", nil
	})
}

func (m model) runClusterApply(spec capi.ClusterSpec) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if err := core.ApplyCluster(ctx, app, spec, p); err != nil {
			return "", err
		}
		return "applied cluster " + spec.Namespace + "/" + spec.Name, nil
	})
}

func (m model) runClusterWrite(spec capi.ClusterSpec) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		path, err := core.WriteClusterManifests(app, spec, p)
		if err != nil {
			return "", err
		}
		return "wrote manifests to " + path, nil
	})
}

func (m model) runClusterDelete(namespace, name string) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if err := core.DeleteCluster(ctx, app, namespace, name, p); err != nil {
			return "", err
		}
		return "deleted cluster " + namespace + "/" + name, nil
	})
}

func (m model) renderClusterPreview(spec capi.ClusterSpec) tea.Cmd {
	return func() tea.Msg {
		data, err := core.RenderCluster(spec)
		return manifestRenderedMsg{data: data, err: err}
	}
}

func (m model) runFlavorApply(req flavorRequest) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if err := core.ApplyFlavor(ctx, app, req.template, req.vars, p); err != nil {
			return "", err
		}
		return "applied flavor cluster " + req.name, nil
	})
}

func (m model) runFlavorWrite(req flavorRequest) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		path, err := core.WriteFlavorManifests(app, req.name, req.template, req.vars, p)
		if err != nil {
			return "", err
		}
		return "wrote manifests to " + path, nil
	})
}

func (m model) renderFlavorPreview(req flavorRequest) tea.Cmd {
	return func() tea.Msg {
		data, err := core.RenderFlavor(req.template, req.vars)
		return manifestRenderedMsg{data: data, err: err}
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

func (m model) loadSchedules() tea.Cmd {
	app := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		vc, err := newVeleroClient(app)
		if err != nil {
			return schedulesLoadedMsg{err: err}
		}
		schedules, err := core.ListSchedules(ctx, vc)
		if err != nil {
			return schedulesLoadedMsg{err: err}
		}
		return schedulesLoadedMsg{schedules: schedules}
	}
}

func (m model) loadStorageLocations() tea.Cmd {
	app := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		vc, err := newVeleroClient(app)
		if err != nil {
			return storageLocationsLoadedMsg{err: err}
		}
		locations, err := core.ListBackupStorageLocations(ctx, vc)
		if err != nil {
			return storageLocationsLoadedMsg{err: err}
		}
		return storageLocationsLoadedMsg{locations: locations}
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
