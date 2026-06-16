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

	snap   core.Snapshot
	loaded bool
	width  int
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
		return m, nil
	case snapshotMsg:
		m.snap = msg.snap
		m.loaded = true
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadSnapshot(), tick())
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Refresh):
			return m, m.loadSnapshot()
		}
	}
	return m, nil
}
