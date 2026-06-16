package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
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
	height int

	mode mode

	input  textinput.Model
	log    logModel
	stream <-chan streamEvent

	spinner spinner.Model
	loading bool

	pending tea.Cmd
	confirm string

	picker themePicker
}

func newModel(app *core.App, contextName string) model {
	ti := textinput.New()
	ti.Prompt = commandPrompt
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(refreshLabelStyle))
	return model{
		app:     app,
		context: contextName,
		input:   ti,
		log:     newLog(1, 1),
		spinner: sp,
		loading: true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadSnapshot(), tick(), m.spinner.Tick)
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
		m.relayout()
		return m, nil
	case snapshotMsg:
		m.snap = msg.snap
		m.loaded = true
		m.loading = false
		m.relayout()
		return m, nil
	case tickMsg:
		m.loading = true
		return m, tea.Batch(m.loadSnapshot(), tick(), m.spinner.Tick)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case streamStartedMsg:
		m.stream = msg.ch
		return m, waitForStream(msg.ch)
	case streamEvent:
		return m.onStreamEvent(msg)
	case detailMsg:
		m.log.append(msg.body)
		return m, nil
	case manifestRenderedMsg:
		return m.onManifestRendered(msg)
	case backupsLoadedMsg:
		return m.onBackupsLoaded(msg)
	case restoresLoadedMsg:
		return m.onRestoresLoaded(msg)
	case clustersLoadedMsg:
		return m.onClustersLoaded(msg)
	case schedulesLoadedMsg:
		return m.onSchedulesLoaded(msg)
	case storageLocationsLoadedMsg:
		return m.onStorageLocationsLoaded(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeCommand:
		return m.onCommandKey(msg)
	case modeConfirm:
		return m.onConfirmKey(msg)
	case modeRunning:
		return m, nil
	case modeHelp:
		m.mode = modeNav
		return m, nil
	case modeThemePicker:
		return m.onThemePickerKey(msg)
	default:
		return m.onNavKey(msg)
	}
}

func (m model) onThemePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.ScrollUp):
		m.picker.moveUp()
		return m, nil
	case key.Matches(msg, keys.ScrollDown):
		m.picker.moveDown()
		return m, nil
	case key.Matches(msg, keys.Confirm):
		return m.commitTheme()
	case key.Matches(msg, keys.Cancel):
		m.picker.revert()
		m.mode = modeNav
		return m, nil
	}
	return m, nil
}

func (m model) commitTheme() (tea.Model, tea.Cmd) {
	pref := m.picker.selected().pref
	m.mode = modeNav
	if err := m.app.Config.SetTheme(pref); err != nil {
		m.picker.revert()
		m.log.append(errStyle.Render("theme: " + err.Error()))
		return m, nil
	}
	applyThemePref(pref)
	if err := m.app.Config.Save(); err != nil {
		m.log.append(dimStyle.Render("theme set to " + pref + " (not persisted: " + err.Error() + ")"))
		return m, nil
	}
	m.log.append(dimStyle.Render("theme set to " + pref))
	return m, nil
}

func (m model) onNavKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Command):
		m.mode = modeCommand
		m.input.SetValue("")
		return m, m.input.Focus()
	case key.Matches(msg, keys.Refresh):
		m.loading = true
		return m, tea.Batch(m.loadSnapshot(), m.spinner.Tick)
	case key.Matches(msg, keys.Help):
		m.mode = modeHelp
		return m, nil
	case key.Matches(msg, keys.ScrollUp):
		m.log.view.ScrollUp(1)
		return m, nil
	case key.Matches(msg, keys.ScrollDown):
		m.log.view.ScrollDown(1)
		return m, nil
	case key.Matches(msg, keys.PageUp):
		m.log.view.PageUp()
		return m, nil
	case key.Matches(msg, keys.PageDown):
		m.log.view.PageDown()
		return m, nil
	}
	return m, nil
}

func (m model) onCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Cancel):
		m.mode = modeNav
		m.input.Blur()
		m.input.SetValue("")
		return m, nil
	case key.Matches(msg, keys.Confirm):
		return m.runInput()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) runInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input.Value())
	m.input.Blur()
	m.input.SetValue("")
	m.mode = modeNav
	if input == "" {
		return m, nil
	}
	m.log.echo(input)
	res := m.dispatch(input)
	for _, line := range res.lines {
		m.log.append(line)
	}
	switch res.builtin {
	case builtinHelp:
		m.mode = modeHelp
		return m, nil
	case builtinThemePicker:
		m.mode = modeThemePicker
		m.picker = newThemePicker(m.app.Config.Theme)
		return m, nil
	case builtinRefresh:
		return m, m.loadSnapshot()
	case builtinClear:
		m.log.clear()
		return m, nil
	case builtinQuit:
		return m, tea.Quit
	}
	if res.cmd == nil {
		return m, nil
	}
	if res.confirm != "" {
		m.mode = modeConfirm
		m.confirm = res.confirm
		m.pending = res.cmd
		m.log.append(warnStyle.Render("confirm: " + res.confirm + " (y/n)"))
		return m, nil
	}
	return m.start(res.cmd)
}

func (m model) onConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		cmd := m.pending
		m.pending = nil
		m.confirm = ""
		return m.start(cmd)
	case "n", "esc":
		m.pending = nil
		m.confirm = ""
		m.mode = modeNav
		m.log.append(dimStyle.Render("cancelled"))
		return m, nil
	}
	return m, nil
}

func (m model) start(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.mode = modeRunning
	m.log.append(dimStyle.Render("running…"))
	return m, cmd
}

func (m model) onStreamEvent(ev streamEvent) (tea.Model, tea.Cmd) {
	if ev.done {
		m.stream = nil
		m.mode = modeNav
		if ev.err != nil {
			m.log.append(errStyle.Render("error: " + ev.err.Error()))
			return m, nil
		}
		if ev.summary != "" {
			m.log.append(ev.summary)
		}
		m.log.append(dimStyle.Render("done"))
		return m, m.loadSnapshot()
	}
	m.log.append(ev.line)
	return m, waitForStream(m.stream)
}

func (m model) onManifestRendered(msg manifestRenderedMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNav
	if msg.err != nil {
		m.log.append(errStyle.Render("error: " + msg.err.Error()))
		return m, nil
	}
	for _, line := range splitLines(string(msg.data)) {
		m.log.append(line)
	}
	return m, nil
}

func (m model) onBackupsLoaded(msg backupsLoadedMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNav
	if msg.err != nil {
		m.log.append(errStyle.Render("error: " + msg.err.Error()))
		return m, nil
	}
	sort.Slice(msg.backups, func(i, j int) bool {
		return msg.backups[i].CreationTimestamp.After(msg.backups[j].CreationTimestamp.Time)
	})
	rows := [][]string{{"NAME", "STATUS", "CREATED", "EXPIRES", "ERRORS"}}
	for i := range msg.backups {
		b := &msg.backups[i]
		rows = append(rows, []string{
			b.Name, phaseOrDash(string(b.Status.Phase)),
			fmtTime(&b.CreationTimestamp), fmtTime(b.Status.Expiration),
			itoa(b.Status.Errors),
		})
	}
	m.log.append(renderLogTable(rows))
	return m, nil
}

func (m model) onRestoresLoaded(msg restoresLoadedMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNav
	if msg.err != nil {
		m.log.append(errStyle.Render("error: " + msg.err.Error()))
		return m, nil
	}
	rows := [][]string{{"NAME", "BACKUP", "STATUS", "WARNINGS", "ERRORS"}}
	for i := range msg.restores {
		r := &msg.restores[i]
		rows = append(rows, []string{
			r.Name, r.Spec.BackupName, phaseOrDash(string(r.Status.Phase)),
			itoa(r.Status.Warnings), itoa(r.Status.Errors),
		})
	}
	m.log.append(renderLogTable(rows))
	return m, nil
}

func (m model) onClustersLoaded(msg clustersLoadedMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNav
	if msg.err != nil {
		m.log.append(errStyle.Render("error: " + msg.err.Error()))
		return m, nil
	}
	rows := [][]string{{"NAME", "PHASE"}}
	for _, c := range msg.clusters {
		rows = append(rows, []string{c.name, c.phase})
	}
	m.log.append(renderLogTable(rows))
	return m, nil
}

func (m model) onSchedulesLoaded(msg schedulesLoadedMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNav
	if msg.err != nil {
		m.log.append(errStyle.Render("error: " + msg.err.Error()))
		return m, nil
	}
	rows := [][]string{{"NAME", "SCHEDULE", "STATUS", "LAST-BACKUP", "PAUSED"}}
	for i := range msg.schedules {
		s := &msg.schedules[i]
		rows = append(rows, []string{
			s.Name, s.Spec.Schedule, phaseOrDash(string(s.Status.Phase)),
			fmtTime(s.Status.LastBackup), fmtBool(s.Spec.Paused),
		})
	}
	m.log.append(renderLogTable(rows))
	return m, nil
}

func (m model) onStorageLocationsLoaded(msg storageLocationsLoadedMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNav
	if msg.err != nil {
		m.log.append(errStyle.Render("error: " + msg.err.Error()))
		return m, nil
	}
	rows := [][]string{{"NAME", "PROVIDER", "BUCKET", "PHASE", "DEFAULT"}}
	for i := range msg.locations {
		l := &msg.locations[i]
		bucket := "-"
		if l.Spec.ObjectStorage != nil {
			bucket = l.Spec.ObjectStorage.Bucket
		}
		rows = append(rows, []string{
			l.Name, l.Spec.Provider, bucket,
			phaseOrDash(string(l.Status.Phase)), fmtBool(l.Spec.Default),
		})
	}
	m.log.append(renderLogTable(rows))
	return m, nil
}
