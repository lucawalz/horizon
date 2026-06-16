package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
)

var autoDarkBackground = true

func applyThemePref(pref string) {
	switch pref {
	case config.ThemeLight:
		lipgloss.SetHasDarkBackground(false)
	case config.ThemeDark:
		lipgloss.SetHasDarkBackground(true)
	default:
		lipgloss.SetHasDarkBackground(autoDarkBackground)
	}
}

func Run(app *core.App, contextName string) error {
	autoDarkBackground = lipgloss.HasDarkBackground()
	if app.Config != nil {
		applyThemePref(app.Config.Theme)
	}
	p := tea.NewProgram(newModel(app, contextName), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
