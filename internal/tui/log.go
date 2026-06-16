package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
)

const (
	logScrollback = 1000
	promptMarker  = "› "
)

type logModel struct {
	view  viewport.Model
	lines []string
}

func newLog(width, height int) logModel {
	return logModel{view: viewport.New(width, height)}
}

func (l *logModel) resize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	l.view.Width = width
	l.view.Height = height
	l.sync()
}

func (l *logModel) append(line string) {
	l.lines = append(l.lines, line)
	if len(l.lines) > logScrollback {
		l.lines = l.lines[len(l.lines)-logScrollback:]
	}
	l.sync()
	l.view.GotoBottom()
}

func (l *logModel) echo(input string) {
	l.append(promptMarker + input)
}

func (l *logModel) clear() {
	l.lines = nil
	l.sync()
}

func (l *logModel) sync() {
	l.view.SetContent(joinLines(l.lines))
}
