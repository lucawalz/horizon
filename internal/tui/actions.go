package tui

import (
	"context"
	"io"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func newVeleroClient(app *core.App) (core.VeleroClient, error) {
	return velero.NewClient(app.Config.Kubeconfig)
}

func (m model) runScaleUp(target core.PoolTarget) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if target.PoolType == core.ElasticPoolType {
			return "", core.ElasticAutoscalerErr()
		}
		hc, spec, err := app.ReservedClient(ctx)
		if err != nil {
			return "", err
		}
		return "", core.ScaleUp(ctx, hc, spec, target, false, p)
	})
}

func (m model) runScaleDown(target core.PoolTarget) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if target.PoolType == core.ElasticPoolType {
			return "", core.ElasticAutoscalerErr()
		}
		hc, spec, err := app.ReservedClient(ctx)
		if err != nil {
			return "", err
		}
		return "", core.ScaleDown(ctx, hc, spec, target, false, p)
	})
}

func (m model) runBurst(params core.BurstParams) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if params.Target.PoolType == core.ElasticPoolType {
			return "", core.ElasticAutoscalerErr()
		}
		hc, spec, err := app.ReservedClient(ctx)
		if err != nil {
			return "", err
		}
		vc, err := newVeleroClient(app)
		if err != nil {
			return "", err
		}
		err = core.Burst(ctx, hc, spec, app.KubeClient, vc, params, p)
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

func validateNamespace(ns string) error {
	return k8s.ValidateNamespace(ns)
}
