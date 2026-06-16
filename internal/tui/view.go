package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.overlay.mode != overlayNone {
		return m.renderOverlay()
	}

	var b strings.Builder
	b.WriteString(renderBanner(m.width, m.app.Cluster, m.context))
	b.WriteString("\n\n")

	if !m.loaded {
		b.WriteString(dimStyle.Render("loading cluster snapshot…"))
		b.WriteString("\n\n")
	} else {
		b.WriteString(renderDashboard(m.snap, m.width))
		b.WriteString("\n\n")
	}

	if m.showHelp {
		b.WriteString(footerStyle.Render(fullHelp()))
	} else {
		b.WriteString(footerStyle.Render(footerHint()))
	}
	return b.String()
}

func footerHint() string {
	return "[u]p [d]own [n]udge [b]urst [c]luster [k]backups [t]restore [x]drain  [r]efresh [?]help [q]uit"
}

func fullHelp() string {
	lines := []string{
		"u  scale pool up        d  scale pool down       n  nudge control plane",
		"b  burst workload       c  cluster create/delete k  backup create/describe/delete",
		"t  restore create/desc  x  drain a node          r  refresh",
		"?  toggle help          q  quit",
		"in overlays: enter confirm/select · esc cancel · tab/shift+tab move",
	}
	return strings.Join(lines, "\n")
}

func (m model) renderOverlay() string {
	var body string
	switch m.overlay.mode {
	case overlayMenu:
		body = m.overlay.menu.View() + "\n\n" + dimStyle.Render("enter select · esc cancel")
	case overlayPicker:
		body = m.overlay.picker.View() + "\n\n" + dimStyle.Render("enter select · / filter · esc cancel")
	case overlayForm:
		body = renderForm(m.overlay.form)
	case overlayConfirm:
		body = warnStyle.Render(m.overlay.confirm.prompt) + "\n\n" + dimStyle.Render("enter confirm · esc cancel")
	case overlayProgress:
		body = m.overlay.title + "\n\n" + m.overlay.log.View() + "\n\n" + dimStyle.Render("running…")
	case overlayManifest:
		body = m.renderManifest()
	case overlayResult:
		body = m.renderResult()
	}
	box := overlayBoxStyle.Render(panelTitleStyle.Render(m.overlay.title) + "\n\n" + body)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

func renderForm(f formState) string {
	var b strings.Builder
	for i := range f.fields {
		marker := "  "
		if i == f.focus {
			marker = "> "
		}
		label := fmt.Sprintf("%-20s", f.fields[i].label)
		b.WriteString(marker + label + f.fields[i].input.View() + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("tab/shift+tab move · enter submit · esc cancel"))
	return b.String()
}

func (m model) renderManifest() string {
	hint := "a apply live"
	if m.overlay.manifest.canWrite {
		hint += " · w write to bedrock"
	} else {
		hint += " · (w disabled: bedrock_path unset)"
	}
	hint += " · esc cancel"
	return m.overlay.manifest.view.View() + "\n\n" + dimStyle.Render(hint)
}

func (m model) renderResult() string {
	if m.overlay.resultErr != nil {
		return errStyle.Render(m.overlay.resultErr.Error()) + "\n\n" + dimStyle.Render("enter/esc dismiss")
	}
	return m.overlay.result + "\n\n" + dimStyle.Render("enter/esc dismiss")
}
