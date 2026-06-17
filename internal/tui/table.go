package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

const (
	minNameWidth = 8
	cellPadding  = 1
	ellipsis     = "…"
)

func middleEllipsis(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len([]rune(s)) <= budget {
		return s
	}
	r := []rune(s)
	if budget <= 1 {
		return ellipsis
	}
	keep := budget - 1
	head := keep / 2
	tail := keep - head
	return string(r[:head]) + ellipsis + string(r[len(r)-tail:])
}

func fitNameColumn(headers []string, rows [][]string, nameCol, inner int) {
	if len(rows) == 0 {
		return
	}
	cols := len(rows[0])
	budget := inner
	for col := 0; col < cols; col++ {
		if col == nameCol {
			budget -= cellPadding
			continue
		}
		max := 0
		if col < len(headers) {
			max = len([]rune(headers[col]))
		}
		for _, row := range rows {
			if w := len([]rune(row[col])); w > max {
				max = w
			}
		}
		budget -= max + cellPadding
	}
	if budget < minNameWidth {
		budget = minNameWidth
	}
	for _, row := range rows {
		row[nameCol] = middleEllipsis(row[nameCol], budget)
	}
}

func tableNaturalWidth(headers []string, rows [][]string) int {
	total := 0
	for col := range headers {
		max := len([]rune(headers[col]))
		for _, row := range rows {
			if col < len(row) {
				if w := len([]rune(row[col])); w > max {
					max = w
				}
			}
		}
		total += max + cellPadding
	}
	return total
}

func renderLogTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	headers := rows[0]
	body := rows[1:]
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderRow(false).
		BorderHeader(false).
		Headers(headers...).
		Rows(body...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return tableHeaderStyle.Padding(0, 1).PaddingLeft(0)
			}
			return tableCellStyle.PaddingLeft(0)
		})
	return t.Render()
}

func newPanelTable(headers []string, width int, styleFunc table.StyleFunc) *table.Table {
	t := table.New().
		Wrap(false).
		Border(lipgloss.NormalBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderRow(false).
		BorderHeader(false).
		Headers(headers...).
		StyleFunc(styleFunc)
	if width > 0 {
		t = t.Width(width)
	}
	return t
}
