package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
)

func Run(app *core.App, contextName string) error {
	p := tea.NewProgram(newModel(app, contextName), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
