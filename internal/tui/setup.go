package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
)

const detectTimeout = 15 * time.Second

type setupStep int

const (
	stepContext setupStep = iota
	stepDetect
	stepFields
	stepTheme
	stepDone
)

type setupInput struct {
	context        string
	cluster        string
	poolsNamespace string
	poolTypesRaw   string
	bedrockPath    string
	ccClass        string
	ccWorkerClass  string
	theme          string
}

func parsePoolTypes(raw string) (map[string]string, error) {
	types := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("pool type %q must be type=mdname", part)
		}
		name := strings.TrimSpace(kv[0])
		md := strings.TrimSpace(kv[1])
		if name == "" || md == "" {
			return nil, fmt.Errorf("pool type %q must be type=mdname", part)
		}
		types[name] = md
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("at least one pool type required")
	}
	return types, nil
}

func formatPoolTypes(types map[string]string) string {
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+types[name])
	}
	return strings.Join(parts, ",")
}

func buildSetupConfig(in setupInput) (*config.Config, error) {
	cfg := config.Default(config.DefaultConfigPath())
	types, err := parsePoolTypes(in.poolTypesRaw)
	if err != nil {
		return nil, err
	}
	cfg.Context = in.context
	cfg.Cluster = in.cluster
	cfg.Pools.Namespace = in.poolsNamespace
	cfg.Pools.Types = types
	cfg.BedrockPath = in.bedrockPath
	cfg.ClusterCreate.Class = in.ccClass
	cfg.ClusterCreate.WorkerClass = in.ccWorkerClass
	if err := cfg.SetTheme(in.theme); err != nil {
		return nil, err
	}
	return cfg, nil
}

type contextsLoadedMsg struct {
	names   []string
	current string
	err     error
}

type detectedMsg struct {
	detected core.Detected
	err      error
}

const (
	fieldCluster = iota
	fieldPoolsNamespace
	fieldPoolTypes
	fieldBedrockPath
	fieldClass
	fieldWorkerClass
	fieldCount
)

var fieldLabels = [fieldCount]string{
	"cluster name",
	"pools namespace",
	"pool types (type=mdname,…)",
	"bedrock path (optional)",
	"cluster_create class (optional)",
	"cluster_create worker_class (optional)",
}

type setupModel struct {
	step   setupStep
	width  int
	height int

	contexts   []string
	ctxCursor  int
	ctxCurrent string
	chosenCtx  string

	spinner spinner.Model

	detected   core.Detected
	detectErr  error
	contextErr error

	fields     [fieldCount]textinput.Model
	fieldIndex int
	fieldErr   string

	picker themePicker

	saved   bool
	savedAt string
	saveErr string
}

func newSetupModel() setupModel {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(refreshLabelStyle))
	m := setupModel{step: stepContext, spinner: sp}
	for i := range m.fields {
		ti := textinput.New()
		m.fields[i] = ti
	}
	return m
}

func RunSetup() (bool, error) {
	silenceKlog()
	autoDarkBackground = lipgloss.HasDarkBackground()
	applyThemePref(config.ThemeAuto)
	p := tea.NewProgram(newSetupModel(), tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return false, err
	}
	final, ok := out.(setupModel)
	if !ok {
		return false, nil
	}
	return final.saved, nil
}

func (m setupModel) Init() tea.Cmd {
	return tea.Batch(loadContexts(), m.spinner.Tick)
}

func loadContexts() tea.Cmd {
	return func() tea.Msg {
		names, current, err := k8s.Contexts("")
		return contextsLoadedMsg{names: names, current: current, err: err}
	}
}

func detectCmd(chosenCtx string) tea.Cmd {
	return func() tea.Msg {
		kube, err := k8s.NewClientForContext("", chosenCtx)
		if err != nil {
			return detectedMsg{err: err}
		}
		cc, err := capi.NewClientForContext("", chosenCtx)
		if err != nil {
			return detectedMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
		defer cancel()
		detected, err := core.Detect(ctx, kube, cc)
		return detectedMsg{detected: detected, err: err}
	}
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case contextsLoadedMsg:
		m.contextErr = msg.err
		m.contexts = msg.names
		m.ctxCurrent = msg.current
		for i, name := range m.contexts {
			if name == m.ctxCurrent {
				m.ctxCursor = i
			}
		}
		return m, nil
	case detectedMsg:
		m.detectErr = msg.err
		if msg.err == nil {
			m.detected = msg.detected
			m.prefillFields()
			m.step = stepFields
			m.fieldIndex = 0
			return m, m.fields[0].Focus()
		}
		return m, nil
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m setupModel) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.step {
	case stepContext:
		return m.onContextKey(msg)
	case stepDetect:
		return m.onDetectKey(msg)
	case stepFields:
		return m.onFieldsKey(msg)
	case stepTheme:
		return m.onThemeKey(msg)
	case stepDone:
		switch msg.String() {
		case "enter", "q", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m setupModel) onContextKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, tea.Quit
	case "up", "k":
		if len(m.contexts) > 0 {
			if m.ctxCursor > 0 {
				m.ctxCursor--
			} else {
				m.ctxCursor = len(m.contexts) - 1
			}
		}
		return m, nil
	case "down", "j":
		if len(m.contexts) > 0 {
			if m.ctxCursor < len(m.contexts)-1 {
				m.ctxCursor++
			} else {
				m.ctxCursor = 0
			}
		}
		return m, nil
	case "enter":
		if len(m.contexts) == 0 {
			return m, tea.Quit
		}
		m.chosenCtx = m.contexts[m.ctxCursor]
		m.step = stepDetect
		m.detectErr = nil
		return m, tea.Batch(detectCmd(m.chosenCtx), m.spinner.Tick)
	}
	return m, nil
}

func (m setupModel) onDetectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.detectErr == nil {
		return m, nil
	}
	switch msg.String() {
	case "r":
		m.detectErr = nil
		return m, tea.Batch(detectCmd(m.chosenCtx), m.spinner.Tick)
	case "esc":
		m.step = stepContext
		m.detectErr = nil
		return m, nil
	}
	return m, nil
}

func (m setupModel) onFieldsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.step = stepContext
		m.blurFields()
		return m, nil
	case "up":
		return m.focusField(m.fieldIndex - 1)
	case "down", "tab":
		return m.focusField(m.fieldIndex + 1)
	case "enter":
		if m.fieldIndex < fieldCount-1 {
			return m.focusField(m.fieldIndex + 1)
		}
		if _, err := parsePoolTypes(m.fields[fieldPoolTypes].Value()); err != nil {
			m.fieldErr = err.Error()
			return m.focusField(fieldPoolTypes)
		}
		m.fieldErr = ""
		m.blurFields()
		m.step = stepTheme
		m.picker = newThemePicker(config.ThemeAuto)
		return m, nil
	}
	var cmd tea.Cmd
	m.fields[m.fieldIndex], cmd = m.fields[m.fieldIndex].Update(msg)
	return m, cmd
}

func (m setupModel) focusField(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 {
		idx = fieldCount - 1
	}
	if idx >= fieldCount {
		idx = 0
	}
	m.blurFields()
	m.fieldIndex = idx
	return m, m.fields[idx].Focus()
}

func (m *setupModel) blurFields() {
	for i := range m.fields {
		m.fields[i].Blur()
	}
}

func (m setupModel) onThemeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.picker.moveUp()
		return m, nil
	case "down", "j":
		m.picker.moveDown()
		return m, nil
	case "esc":
		m.step = stepFields
		return m, m.fields[m.fieldIndex].Focus()
	case "enter":
		return m.save()
	}
	return m, nil
}

func (m setupModel) save() (tea.Model, tea.Cmd) {
	in := setupInput{
		context:        m.chosenCtx,
		cluster:        m.fields[fieldCluster].Value(),
		poolsNamespace: m.fields[fieldPoolsNamespace].Value(),
		poolTypesRaw:   m.fields[fieldPoolTypes].Value(),
		bedrockPath:    m.fields[fieldBedrockPath].Value(),
		ccClass:        m.fields[fieldClass].Value(),
		ccWorkerClass:  m.fields[fieldWorkerClass].Value(),
		theme:          m.picker.selected().pref,
	}
	cfg, err := buildSetupConfig(in)
	if err != nil {
		m.step = stepFields
		m.fieldErr = err.Error()
		return m.focusField(fieldPoolTypes)
	}
	if err := cfg.Save(); err != nil {
		m.saveErr = err.Error()
		m.step = stepDone
		return m, nil
	}
	applyThemePref(cfg.Theme)
	m.saved = true
	m.savedAt = cfg.Path()
	m.step = stepDone
	return m, nil
}

func (m *setupModel) prefillFields() {
	def := config.Default(config.DefaultConfigPath())
	cluster := m.detected.ClusterName
	if cluster == "" {
		cluster = def.Cluster
	}
	ns := m.detected.PoolsNamespace
	if ns == "" {
		ns = def.Pools.Namespace
	}
	types := m.detected.PoolTypes
	if len(types) == 0 {
		types = def.Pools.Types
	}
	m.fields[fieldCluster].SetValue(cluster)
	m.fields[fieldPoolsNamespace].SetValue(ns)
	m.fields[fieldPoolTypes].SetValue(formatPoolTypes(types))
	m.fields[fieldBedrockPath].SetValue("")
	m.fields[fieldClass].SetValue(def.ClusterCreate.Class)
	m.fields[fieldWorkerClass].SetValue(def.ClusterCreate.WorkerClass)
}

func (m setupModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	box := modalStyle.Render(m.content())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m setupModel) content() string {
	switch m.step {
	case stepContext:
		return m.contextView()
	case stepDetect:
		return m.detectView()
	case stepFields:
		return m.fieldsView()
	case stepTheme:
		return m.themeView()
	default:
		return m.doneView()
	}
}

func (m setupModel) contextView() string {
	if m.contextErr != nil {
		return lipgloss.JoinVertical(lipgloss.Left,
			helpTitleStyle.Render("horizon setup"),
			errStyle.Render("kubeconfig: "+m.contextErr.Error()),
			"",
			dimStyle.Render("esc quit"),
		)
	}
	if len(m.contexts) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			helpTitleStyle.Render("horizon setup"),
			dimStyle.Render("loading contexts…"),
		)
	}
	rows := make([]string, 0, len(m.contexts))
	for i, name := range m.contexts {
		line := pickerCursorIndent + name
		if i == m.ctxCursor {
			line = helpCommandStyle.Render(pickerCursor + name)
		}
		if name == m.ctxCurrent {
			line += dimStyle.Render(pickerActiveMark)
		}
		rows = append(rows, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		helpTitleStyle.Render("select context"),
		strings.Join(rows, "\n"),
		"",
		dimStyle.Render("↑↓ select · enter confirm · esc quit"),
	)
}

func (m setupModel) detectView() string {
	if m.detectErr != nil {
		return lipgloss.JoinVertical(lipgloss.Left,
			helpTitleStyle.Render("detect"),
			errStyle.Render(m.detectErr.Error()),
			"",
			dimStyle.Render("r retry · esc back"),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		helpTitleStyle.Render("detect"),
		m.spinner.View()+refreshLabelStyle.Render(" inspecting "+m.chosenCtx+"…"),
	)
}

func (m setupModel) fieldsView() string {
	rows := make([]string, 0, fieldCount)
	for i := range m.fields {
		marker := pickerCursorIndent
		if i == m.fieldIndex {
			marker = helpCommandStyle.Render(pickerCursor)
		}
		label := dimStyle.Render(fieldLabels[i])
		rows = append(rows, marker+label)
		rows = append(rows, pickerCursorIndent+m.fields[i].View())
	}
	parts := []string{helpTitleStyle.Render("configure"), strings.Join(rows, "\n")}
	if m.fieldErr != "" {
		parts = append(parts, errStyle.Render(m.fieldErr))
	}
	parts = append(parts, "", dimStyle.Render("↑↓/tab move · enter next · esc back"))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m setupModel) themeView() string {
	rows := make([]string, 0, len(themeOptions))
	for i, opt := range themeOptions {
		line := pickerCursorIndent + opt.label
		if i == m.picker.cursor {
			line = helpCommandStyle.Render(pickerCursor + opt.label)
		}
		rows = append(rows, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		helpTitleStyle.Render("theme"),
		strings.Join(rows, "\n"),
		"",
		dimStyle.Render("↑↓ select · enter save · esc back"),
	)
}

func (m setupModel) doneView() string {
	if m.saveErr != "" {
		return lipgloss.JoinVertical(lipgloss.Left,
			helpTitleStyle.Render("setup"),
			errStyle.Render("save failed: "+m.saveErr),
			"",
			dimStyle.Render("enter quit"),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		helpTitleStyle.Render("setup complete"),
		dimStyle.Render("configuration written to "+m.savedAt),
		"",
		dimStyle.Render("enter quit"),
	)
}
