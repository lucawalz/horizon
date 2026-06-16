package tui

import (
	"fmt"
	"strings"
)

func (m model) View() string {
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

	b.WriteString(footerStyle.Render(footerHint()))
	return b.String()
}

func footerHint() string {
	return fmt.Sprintf("[%s]efresh  [%s]uit", keys.Refresh.Help().Key, keys.Quit.Help().Key)
}
