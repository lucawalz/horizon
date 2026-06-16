package tui

import (
	"flag"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
	"k8s.io/klog/v2"
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

func silenceKlog() {
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	_ = fs.Set("logtostderr", "false")
	_ = fs.Set("alsologtostderr", "false")
	_ = fs.Set("stderrthreshold", "FATAL")
	klog.SetOutput(io.Discard)
}

func Run(app *core.App) error {
	silenceKlog()
	autoDarkBackground = lipgloss.HasDarkBackground()
	if app.Config != nil {
		applyThemePref(app.Config.Theme)
	}
	p := tea.NewProgram(newModel(app), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
