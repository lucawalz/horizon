package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) openForm(f formState) model {
	m.overlay = overlayState{mode: overlayForm, title: formTitle(f.action), form: f}
	return m
}

func (m model) openConfirm(prompt string, run tea.Cmd) model {
	m.overlay.mode = overlayConfirm
	m.overlay.confirm = confirmState{prompt: prompt, confirm: run}
	return m
}

func (m model) startProgress(cmd tea.Cmd) (model, tea.Cmd) {
	m.overlay = overlayState{mode: overlayProgress, title: m.overlay.title}
	m.overlay.log = newProgressViewport()
	return m, cmd
}

func (m model) showResult(summary string, err error) model {
	m.overlay.mode = overlayResult
	m.overlay.result = summary
	m.overlay.resultErr = err
	return m
}

func (m model) dismiss() model {
	m.overlay = overlayState{}
	return m
}

func (m model) openClusterMenu() model {
	items := []list.Item{
		menuItem{title: "Create", desc: "render and apply or write a cluster", action: actionClusterCreate},
		menuItem{title: "Delete", desc: "delete a managed cluster", action: actionClusterDelete},
	}
	m.overlay = overlayState{mode: overlayMenu, title: "Cluster", menu: newMenu("Cluster", items)}
	return m
}

func (m model) openBackupMenu() model {
	items := []list.Item{
		menuItem{title: "Create", desc: "create a velero backup", action: actionBackupCreate},
		menuItem{title: "Describe", desc: "show backup detail", action: actionBackupDescribe},
		menuItem{title: "Delete", desc: "delete a backup", action: actionBackupDelete},
	}
	m.overlay = overlayState{mode: overlayMenu, title: "Backups", menu: newMenu("Backups", items)}
	return m
}

func (m model) openRestoreMenu() model {
	items := []list.Item{
		menuItem{title: "Create", desc: "restore from a backup", action: actionRestoreCreate},
		menuItem{title: "Describe", desc: "show restore detail", action: actionRestoreDescribe},
	}
	m.overlay = overlayState{mode: overlayMenu, title: "Restore", menu: newMenu("Restore", items)}
	return m
}

func (m model) openNodePicker() model {
	items := make([]list.Item, 0, len(m.snap.Nodes))
	for _, n := range m.snap.Nodes {
		items = append(items, pickerItem{name: n.Name, desc: n.Role + " · " + n.Status})
	}
	m.overlay = overlayState{mode: overlayPicker, title: "Drain node", picker: newPicker("Drain node", items)}
	m.overlay.form.action = actionDrain
	return m
}

func formTitle(a actionKind) string {
	switch a {
	case actionPoolUp:
		return "Pool up"
	case actionPoolDown:
		return "Pool down"
	case actionNudge:
		return "Nudge control plane"
	case actionBurst:
		return "Burst"
	case actionBackupCreate:
		return "Create backup"
	case actionRestoreCreate:
		return "Create restore"
	case actionClusterCreate:
		return "Create cluster"
	default:
		return "Action"
	}
}
