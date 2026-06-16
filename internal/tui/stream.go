package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
)

type streamEvent struct {
	line    string
	summary string
	done    bool
	debug   bool
	err     error
}

type streamStartedMsg struct {
	ch <-chan streamEvent
}

type actionFunc func(ctx context.Context, p core.Progress) (summary string, err error)

func streamCmd(debug bool, fn actionFunc) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan streamEvent, progressBuffer)
		go func() {
			defer close(ch)
			ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
			defer cancel()
			emitSink := func(line string) { ch <- streamEvent{line: line} }
			var debugSink func(string)
			if debug {
				debugSink = func(line string) { ch <- streamEvent{line: line, debug: true} }
				restore := k8s.SetAPITrace(debugSink)
				defer restore()
			}
			progress := core.NewProgress(emitSink, debugSink)
			summary, err := fn(ctx, progress)
			ch <- streamEvent{done: true, summary: summary, err: err}
		}()
		return streamStartedMsg{ch: ch}
	}
}

func waitForStream(ch <-chan streamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamEvent{done: true}
		}
		return ev
	}
}
