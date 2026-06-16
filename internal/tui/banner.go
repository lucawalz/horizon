package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

const bannerArt = `██╗  ██╗ ██████╗ ██████╗ ██╗███████╗ ██████╗ ███╗   ██╗
██║  ██║██╔═══██╗██╔══██╗██║╚══███╔╝██╔═══██╗████╗  ██║
███████║██║   ██║██████╔╝██║  ███╔╝ ██║   ██║██╔██╗ ██║
██╔══██║██║   ██║██╔══██╗██║ ███╔╝  ██║   ██║██║╚██╗██║
██║  ██║╚██████╔╝██║  ██║██║███████╗╚██████╔╝██║ ╚████║
╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝╚═╝╚══════╝ ╚═════╝ ╚═╝  ╚═══╝`

func renderBanner(width int, cluster, context string) string {
	lines := strings.Split(bannerArt, "\n")
	cols := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > cols {
			cols = n
		}
	}

	start, _ := colorful.Hex(gradientStartHex)
	end, _ := colorful.Hex(gradientEndHex)

	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		runes := []rune(line)
		var b strings.Builder
		for i, r := range runes {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			t := 0.0
			if cols > 1 {
				t = float64(i) / float64(cols-1)
			}
			hex := start.BlendLab(end, t).Clamped().Hex()
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(string(r)))
		}
		out = append(out, b.String())
	}

	sub := fmt.Sprintf("cluster command centre · %s · ctx:%s", valueOr(cluster, "default"), valueOr(context, "current"))
	out = append(out, dimStyle.Render(sub))

	banner := strings.Join(out, "\n")
	if width > 0 {
		return lipgloss.NewStyle().Width(width).Render(banner)
	}
	return banner
}

func valueOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
