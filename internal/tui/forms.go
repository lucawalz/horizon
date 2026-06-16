package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type formField struct {
	label string
	input textinput.Model
}

type formState struct {
	action  actionKind
	fields  []formField
	focus   int
	picked  string
	confirm func(m model, values map[string]string) (string, tea.Cmd, error)
}

func (f *formState) value(label string) string {
	for i := range f.fields {
		if f.fields[i].label == label {
			return f.fields[i].input.Value()
		}
	}
	return ""
}

func (f *formState) values() map[string]string {
	out := make(map[string]string, len(f.fields))
	for i := range f.fields {
		out[f.fields[i].label] = f.fields[i].input.Value()
	}
	return out
}

func (f *formState) focusField(idx int) tea.Cmd {
	var cmd tea.Cmd
	for i := range f.fields {
		if i == idx {
			cmd = f.fields[i].input.Focus()
		} else {
			f.fields[i].input.Blur()
		}
	}
	f.focus = idx
	return cmd
}

func (f *formState) advance(delta int) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	next := (f.focus + delta + len(f.fields)) % len(f.fields)
	return f.focusField(next)
}

func newForm(action actionKind, fields []formField, confirm func(m model, values map[string]string) (string, tea.Cmd, error)) formState {
	f := formState{action: action, fields: fields, confirm: confirm}
	f.focusField(0)
	return f
}
