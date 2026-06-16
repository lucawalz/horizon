package tui

import "strings"

const columnGap = 2

func renderTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for i, cell := range r {
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	for ri, r := range rows {
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			text := cell
			if i < cols-1 {
				text = pad(cell, widths[i]+columnGap)
			}
			if ri == 0 {
				text = tableHeaderStyle.Render(text)
			}
			b.WriteString(text)
		}
		if ri < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func pad(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}
