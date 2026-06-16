package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
)

const snapshotTimeout = 30 * time.Second

type snapshotMsg struct{ snap core.Snapshot }

type tickMsg struct{}

type model struct {
	app     *core.App
	context string

	snap     core.Snapshot
	loaded   bool
	width    int
	height   int
	showHelp bool

	overlay overlayState
}

func newModel(app *core.App, contextName string) model {
	return model{app: app, context: contextName}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadSnapshot(), tick())
}

func (m model) loadSnapshot() tea.Cmd {
	app := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		return snapshotMsg{snap: core.BuildSnapshot(ctx, app)}
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case snapshotMsg:
		m.snap = msg.snap
		m.loaded = true
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadSnapshot(), tick())
	}

	if m.overlay.mode != overlayNone {
		return m.updateOverlay(msg)
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		return m.updateBase(km)
	}
	return m, nil
}

func (m model) updateBase(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, keys.Refresh):
		return m, m.loadSnapshot()
	case key.Matches(msg, keys.Up):
		return m.openForm(m.poolUpForm()), nil
	case key.Matches(msg, keys.Down):
		return m.openForm(m.poolDownForm()), nil
	case key.Matches(msg, keys.Nudge):
		return m.openForm(m.nudgeForm()), nil
	case key.Matches(msg, keys.Burst):
		return m.openForm(m.burstForm()), nil
	case key.Matches(msg, keys.Drain):
		return m.openNodePicker(), nil
	case key.Matches(msg, keys.Cluster):
		return m.openClusterMenu(), nil
	case key.Matches(msg, keys.Backup):
		return m.openBackupMenu(), nil
	case key.Matches(msg, keys.Restore):
		return m.openRestoreMenu(), nil
	}
	return m, nil
}
