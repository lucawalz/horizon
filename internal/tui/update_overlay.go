package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) updateOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case streamStartedMsg:
		m.overlay.stream = msg.ch
		return m, waitForStream(msg.ch)
	case streamEvent:
		return m.onStreamEvent(msg)
	case backupsLoadedMsg:
		return m.onBackupsLoaded(msg)
	case restoresLoadedMsg:
		return m.onRestoresLoaded(msg)
	case clustersLoadedMsg:
		return m.onClustersLoaded(msg)
	case manifestRenderedMsg:
		return m.onManifestRendered(msg)
	case tea.KeyMsg:
		return m.onOverlayKey(msg)
	}
	return m, nil
}

func (m model) onOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Cancel) && m.overlay.mode != overlayProgress {
		return m.dismiss(), nil
	}
	switch m.overlay.mode {
	case overlayMenu:
		return m.onMenuKey(msg)
	case overlayForm:
		return m.onFormKey(msg)
	case overlayPicker:
		return m.onPickerKey(msg)
	case overlayConfirm:
		return m.onConfirmKey(msg)
	case overlayManifest:
		return m.onManifestKey(msg)
	case overlayResult:
		if key.Matches(msg, keys.Confirm) || key.Matches(msg, keys.Cancel) {
			refresh := m.loadSnapshot()
			return m.dismiss(), refresh
		}
	}
	return m, nil
}

func (m model) onMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Confirm) {
		item, ok := m.overlay.menu.SelectedItem().(menuItem)
		if !ok {
			return m, nil
		}
		return m.dispatchMenu(item.action)
	}
	var cmd tea.Cmd
	m.overlay.menu, cmd = m.overlay.menu.Update(msg)
	return m, cmd
}

func (m model) dispatchMenu(a actionKind) (tea.Model, tea.Cmd) {
	switch a {
	case actionClusterCreate:
		return m.openForm(m.clusterCreateForm()), nil
	case actionClusterDelete:
		m.overlay = overlayState{mode: overlayPicker, title: "Delete cluster", picker: newPicker("Delete cluster", nil)}
		m.overlay.form.action = actionClusterDelete
		return m, m.loadClusters()
	case actionBackupCreate:
		return m.openForm(m.backupCreateForm()), nil
	case actionBackupDescribe:
		m.overlay = overlayState{mode: overlayPicker, title: "Describe backup", picker: newPicker("Describe backup", nil)}
		m.overlay.form.action = actionBackupDescribe
		return m, m.loadBackups()
	case actionBackupDelete:
		m.overlay = overlayState{mode: overlayPicker, title: "Delete backup", picker: newPicker("Delete backup", nil)}
		m.overlay.form.action = actionBackupDelete
		return m, m.loadBackups()
	case actionRestoreCreate:
		m.overlay = overlayState{mode: overlayPicker, title: "Restore from backup", picker: newPicker("Restore from backup", nil)}
		m.overlay.form.action = actionRestoreCreate
		return m, m.loadBackups()
	case actionRestoreDescribe:
		m.overlay = overlayState{mode: overlayPicker, title: "Describe restore", picker: newPicker("Describe restore", nil)}
		m.overlay.form.action = actionRestoreDescribe
		return m, m.loadRestores()
	}
	return m, nil
}

func (m model) onFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm):
		return m.submitForm()
	case key.Matches(msg, keys.Next):
		cmd := m.overlay.form.advance(1)
		return m, cmd
	case key.Matches(msg, keys.Prev):
		cmd := m.overlay.form.advance(-1)
		return m, cmd
	}
	idx := m.overlay.form.focus
	var cmd tea.Cmd
	m.overlay.form.fields[idx].input, cmd = m.overlay.form.fields[idx].input.Update(msg)
	return m, cmd
}

func (m model) submitForm() (tea.Model, tea.Cmd) {
	prompt, cmd, err := m.overlay.form.confirm(m, m.overlay.form.values())
	if err != nil {
		return m.showResult("", err), nil
	}
	if m.overlay.form.action == actionClusterCreate {
		m.overlay.mode = overlayProgress
		m.overlay.title = "Rendering cluster"
		m.overlay.log = newProgressViewport()
		return m, cmd
	}
	return m.openConfirm(prompt, cmd), nil
}

func (m model) onPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Confirm) && !m.overlay.picker.SettingFilter() {
		item, ok := m.overlay.picker.SelectedItem().(pickerItem)
		if !ok {
			return m, nil
		}
		return m.onPicked(item.name)
	}
	var cmd tea.Cmd
	m.overlay.picker, cmd = m.overlay.picker.Update(msg)
	return m, cmd
}

func (m model) onPicked(name string) (tea.Model, tea.Cmd) {
	switch m.overlay.form.action {
	case actionDrain:
		return m.openConfirm(fmt.Sprintf("Drain node %q (cordon and evict pods)?", name), m.runDrain(name)), nil
	case actionClusterDelete:
		ns := m.app.Config.Pools.Namespace
		return m.openConfirm(fmt.Sprintf("Delete cluster %s/%s?", ns, name), m.runClusterDelete(ns, name)), nil
	case actionBackupDelete:
		return m.openConfirm(fmt.Sprintf("Delete backup %q?", name), m.runBackupDelete(name)), nil
	case actionBackupDescribe:
		return m.showDetail("Backup "+name, m.describeBackup(name)), nil
	case actionRestoreDescribe:
		return m.showDetail("Restore "+name, m.describeRestore(name)), nil
	case actionRestoreCreate:
		return m.openForm(m.restoreCreateForm(name)), nil
	}
	return m, nil
}

func (m model) onConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Confirm) {
		return m.startProgress(m.overlay.confirm.confirm)
	}
	return m, nil
}

func (m model) onManifestKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "a":
		return m.startProgress(m.runClusterApply(m.overlay.manifest.spec))
	case "w":
		if !m.overlay.manifest.canWrite {
			return m, nil
		}
		return m.startProgress(m.runClusterWrite(m.overlay.manifest.spec))
	}
	var cmd tea.Cmd
	m.overlay.manifest.view, cmd = m.overlay.manifest.view.Update(msg)
	return m, cmd
}

func (m model) onStreamEvent(ev streamEvent) (tea.Model, tea.Cmd) {
	if ev.done {
		return m.showResult(ev.summary, ev.err), nil
	}
	m.overlay.logLines = append(m.overlay.logLines, ev.line)
	m.overlay.log.SetContent(joinLines(m.overlay.logLines))
	m.overlay.log.GotoBottom()
	return m, waitForStream(m.overlay.stream)
}

func (m model) onBackupsLoaded(msg backupsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.showResult("", msg.err), nil
	}
	items := make([]list.Item, 0, len(msg.backups))
	for i := range msg.backups {
		b := &msg.backups[i]
		items = append(items, pickerItem{name: b.Name, desc: phaseOrDash(string(b.Status.Phase))})
	}
	return m, m.overlay.picker.SetItems(items)
}

func (m model) onRestoresLoaded(msg restoresLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.showResult("", msg.err), nil
	}
	items := make([]list.Item, 0, len(msg.restores))
	for i := range msg.restores {
		r := &msg.restores[i]
		items = append(items, pickerItem{name: r.Name, desc: r.Spec.BackupName + " · " + phaseOrDash(string(r.Status.Phase))})
	}
	return m, m.overlay.picker.SetItems(items)
}

func (m model) onClustersLoaded(msg clustersLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.showResult("", msg.err), nil
	}
	items := make([]list.Item, 0, len(msg.clusters))
	for _, c := range msg.clusters {
		items = append(items, pickerItem{name: c.name, desc: c.phase})
	}
	return m, m.overlay.picker.SetItems(items)
}

func (m model) onManifestRendered(msg manifestRenderedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.showResult("", msg.err), nil
	}
	view := newManifestViewport()
	view.SetContent(string(msg.data))
	m.overlay = overlayState{
		mode:  overlayManifest,
		title: "Cluster manifest",
		manifest: manifestState{
			view:     view,
			spec:     msg.spec,
			canWrite: m.app.Config.BedrockPath != "",
		},
	}
	return m, nil
}

func (m model) showDetail(title, body string) model {
	m.overlay.mode = overlayResult
	m.overlay.title = title
	m.overlay.result = body
	m.overlay.resultErr = nil
	return m
}
