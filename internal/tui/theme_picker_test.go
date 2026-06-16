package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/config"
)

func TestThemeNoArgOpensPicker(t *testing.T) {
	m := testModel()
	if got := m.dispatch("theme").builtin; got != builtinThemePicker {
		t.Errorf("dispatch(theme).builtin = %v, want %v", got, builtinThemePicker)
	}
}

func TestThemeWithArgSetsDirectly(t *testing.T) {
	m := testModel()
	res := m.dispatch("theme dark")
	if res.builtin != builtinNone {
		t.Errorf("theme dark should not open the picker, got builtin %v", res.builtin)
	}
	if m.app.Config.Theme != config.ThemeDark {
		t.Errorf("config theme = %q, want %q", m.app.Config.Theme, config.ThemeDark)
	}
}

func TestThemePickerNavigationWraps(t *testing.T) {
	p := newThemePicker(config.ThemeAuto)
	if p.selected().pref != config.ThemeAuto {
		t.Fatalf("initial selection = %q, want %q", p.selected().pref, config.ThemeAuto)
	}
	p.moveUp()
	if p.selected().pref != themeOptions[len(themeOptions)-1].pref {
		t.Errorf("moveUp from first did not wrap to last")
	}
	p.moveDown()
	if p.selected().pref != config.ThemeAuto {
		t.Errorf("moveDown did not wrap back to first")
	}
}

func TestThemePickerRevertRestoresBackground(t *testing.T) {
	lipgloss.SetHasDarkBackground(true)
	p := newThemePicker(config.ThemeDark)
	p.moveDown()
	p.moveDown()
	if !p.originalDark {
		t.Fatalf("expected captured original to be dark")
	}
	p.revert()
	if !lipgloss.HasDarkBackground() {
		t.Errorf("revert did not restore dark background")
	}
}
