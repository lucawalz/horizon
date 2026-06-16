package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Refresh key.Binding
	Quit    key.Binding
}

var keys = keyMap{
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}
