package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/capi"
)

type overlayMode int

const (
	overlayNone overlayMode = iota
	overlayMenu
	overlayForm
	overlayPicker
	overlayConfirm
	overlayProgress
	overlayManifest
	overlayResult
)

type actionKind int

const (
	actionPoolUp actionKind = iota
	actionPoolDown
	actionNudge
	actionBurst
	actionDrain
	actionClusterCreate
	actionClusterDelete
	actionBackupCreate
	actionBackupDelete
	actionBackupDescribe
	actionRestoreCreate
	actionRestoreDescribe
)

type menuItem struct {
	title  string
	desc   string
	action actionKind
}

func (i menuItem) Title() string       { return i.title }
func (i menuItem) Description() string { return i.desc }
func (i menuItem) FilterValue() string { return i.title }

type pickerItem struct {
	name string
	desc string
}

func (i pickerItem) Title() string       { return i.name }
func (i pickerItem) Description() string { return i.desc }
func (i pickerItem) FilterValue() string { return i.name }

const (
	overlayWidth      = 72
	menuListHeight    = 10
	pickerListHeight  = 12
	progressViewLines = 14
)

type overlayState struct {
	mode     overlayMode
	title    string
	menu     list.Model
	picker   list.Model
	form     formState
	confirm  confirmState
	manifest manifestState

	log      viewport.Model
	logLines []string
	stream   <-chan streamEvent

	result    string
	resultErr error
}

type confirmState struct {
	prompt  string
	confirm tea.Cmd
}

type manifestState struct {
	view     viewport.Model
	spec     capi.ClusterSpec
	canWrite bool
}

func newMenu(title string, items []list.Item) list.Model {
	l := list.New(items, list.NewDefaultDelegate(), overlayWidth, menuListHeight)
	l.Title = title
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(true)
	return l
}

func newPicker(title string, items []list.Item) list.Model {
	l := list.New(items, list.NewDefaultDelegate(), overlayWidth, pickerListHeight)
	l.Title = title
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	return l
}

func newProgressViewport() viewport.Model {
	return viewport.New(overlayWidth, progressViewLines)
}

func newManifestViewport() viewport.Model {
	return viewport.New(overlayWidth, progressViewLines)
}

func newTextInput(placeholder, value string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.SetValue(value)
	return ti
}
