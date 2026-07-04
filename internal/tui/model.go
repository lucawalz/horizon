package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
)

const snapshotTimeout = 30 * time.Second

const debugLinePrefix = "» "

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

	debug bool

	picker themePicker
}

func newModel(app *core.App) model {
	ti := textinput.New()
	ti.Prompt = commandPrompt
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(refreshLabelStyle))
	return model{
		app:     app,
		context: app.Context,
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
		if m.stream != nil {
			return m, tick()
		}
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
	if ev.debug {
		m.log.append(dimStyle.Render(debugLinePrefix + ev.line))
	} else {
		m.log.append(ev.line)
	}
	return m, waitForStream(m.stream)
}
