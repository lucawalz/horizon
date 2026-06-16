package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/config"
)

const (
	pickerCursor       = "> "
	pickerCursorIndent = "  "
	pickerActiveMark   = " (current)"
	previewGaugeWidth  = 16
	previewGaugeScore  = 0.62
)

type themeOption struct {
	label string
	pref  string
}

var themeOptions = []themeOption{
	{"Auto (match terminal)", config.ThemeAuto},
	{"Dark", config.ThemeDark},
	{"Light", config.ThemeLight},
}

type themePicker struct {
	cursor       int
	originalPref string
	originalDark bool
}

func newThemePicker(activePref string) themePicker {
	p := themePicker{
		originalPref: activePref,
		originalDark: lipgloss.HasDarkBackground(),
	}
	for i, opt := range themeOptions {
		if opt.pref == activePref {
			p.cursor = i
		}
	}
	p.applyPreview()
	return p
}

func (p themePicker) selected() themeOption {
	return themeOptions[p.cursor]
}

func (p *themePicker) moveUp() {
	if p.cursor > 0 {
		p.cursor--
	} else {
		p.cursor = len(themeOptions) - 1
	}
	p.applyPreview()
}

func (p *themePicker) moveDown() {
	if p.cursor < len(themeOptions)-1 {
		p.cursor++
	} else {
		p.cursor = 0
	}
	p.applyPreview()
}

func (p themePicker) applyPreview() {
	applyThemePref(p.selected().pref)
}

func (p themePicker) revert() {
	lipgloss.SetHasDarkBackground(p.originalDark)
}

func (p themePicker) view(width, height int) string {
	rows := make([]string, 0, len(themeOptions))
	for i, opt := range themeOptions {
		line := pickerCursorIndent + opt.label
		if i == p.cursor {
			line = helpCommandStyle.Render(pickerCursor + opt.label)
		}
		if opt.pref == p.originalPref {
			line += dimStyle.Render(pickerActiveMark)
		}
		rows = append(rows, line)
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		helpTitleStyle.Render("theme"),
		strings.Join(rows, "\n"),
		"",
		themePreviewSample(),
		"",
		dimStyle.Render("↑↓ select · enter apply · esc cancel"),
	)
	box := modalStyle.Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func themePreviewSample() string {
	bar := progress.New(
		progress.WithSolidFill(adaptiveHex(theme.DotYellow)),
		progress.WithWidth(previewGaugeWidth),
		progress.WithoutPercentage(),
	)
	bar.EmptyColor = adaptiveHex(theme.GaugeBg)
	heading := lipgloss.JoinHorizontal(lipgloss.Left,
		panelTitleStyle.Render("HORIZON"),
		dimStyle.Render("  cluster command centre"),
	)
	statuses := lipgloss.JoinHorizontal(lipgloss.Left,
		statusDot(theme.DotGreen)+" Ready",
		"   ",
		statusDot(theme.DotRed)+" NotReady",
		"   ",
		errStyle.Render("error"),
	)
	return lipgloss.JoinVertical(lipgloss.Left,
		subLabelStyle.Render("preview"),
		heading,
		statuses,
		gaugeLabelStyle.Render("Load")+" "+bar.ViewAs(previewGaugeScore),
	)
}
