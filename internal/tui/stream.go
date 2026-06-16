package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
)

type streamEvent struct {
	line    string
	summary string
	done    bool
	err     error
}

type streamStartedMsg struct {
	ch <-chan streamEvent
}

type actionFunc func(ctx context.Context, p core.Progress) (summary string, err error)

func streamCmd(fn actionFunc) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan streamEvent, progressBuffer)
		go func() {
			defer close(ch)
			ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
			defer cancel()
			progress := core.Progress(func(line string) {
				ch <- streamEvent{line: line}
			})
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
