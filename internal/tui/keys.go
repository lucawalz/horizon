package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Nudge   key.Binding
	Burst   key.Binding
	Cluster key.Binding
	Backup  key.Binding
	Restore key.Binding
	Drain   key.Binding
	Refresh key.Binding
	Help    key.Binding
	Quit    key.Binding

	Confirm key.Binding
	Cancel  key.Binding
	Next    key.Binding
	Prev    key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "pool up"),
	),
	Down: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "pool down"),
	),
	Nudge: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "nudge"),
	),
	Burst: key.NewBinding(
		key.WithKeys("b"),
		key.WithHelp("b", "burst"),
	),
	Cluster: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "cluster"),
	),
	Backup: key.NewBinding(
		key.WithKeys("k"),
		key.WithHelp("k", "backups"),
	),
	Restore: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "restore"),
	),
	Drain: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "drain"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Confirm: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
	Next: key.NewBinding(
		key.WithKeys("tab", "down"),
		key.WithHelp("tab", "next field"),
	),
	Prev: key.NewBinding(
		key.WithKeys("shift+tab", "up"),
		key.WithHelp("shift+tab", "prev field"),
	),
}
