package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

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
