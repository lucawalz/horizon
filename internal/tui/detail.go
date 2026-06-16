package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type detailMsg struct{ body string }

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

func (m model) describeBackupCmd(name string) tea.Cmd {
	return func() tea.Msg { return detailMsg{body: m.describeBackup(name)} }
}

func (m model) describeRestoreCmd(name string) tea.Cmd {
	return func() tea.Msg { return detailMsg{body: m.describeRestore(name)} }
}

func phaseOrDash(phase string) string {
	if phase == "" {
		return "-"
	}
	return phase
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ",")
}

func fmtTime(t *metav1.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Format(core.BackupTimeLayout)
}

func fmtNamespaceMapping(mp map[string]string) string {
	if len(mp) == 0 {
		return "-"
	}
	pairs := make([]string, 0, len(mp))
	for k, v := range mp {
		pairs = append(pairs, k+":"+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (m model) describeBackup(name string) string {
	vc, err := newVeleroClient(m.app)
	if err != nil {
		return err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	b, err := core.GetBackup(ctx, vc, name)
	if err != nil {
		return err.Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Name:              %s\n", b.Name)
	fmt.Fprintf(&sb, "Phase:             %s\n", phaseOrDash(string(b.Status.Phase)))
	fmt.Fprintf(&sb, "Namespaces:        %s\n", joinOrDash(b.Spec.IncludedNamespaces))
	fmt.Fprintf(&sb, "Storage Location:  %s\n", b.Spec.StorageLocation)
	fmt.Fprintf(&sb, "Created:           %s\n", fmtTime(&b.CreationTimestamp))
	fmt.Fprintf(&sb, "Expires:           %s\n", fmtTime(b.Status.Expiration))
	fmt.Fprintf(&sb, "Errors:            %d\n", b.Status.Errors)
	fmt.Fprintf(&sb, "Warnings:          %d\n", b.Status.Warnings)
	fmt.Fprintf(&sb, "ValidationErrors:  %s", joinOrDash(b.Status.ValidationErrors))
	return sb.String()
}

func (m model) describeRestore(name string) string {
	vc, err := newVeleroClient(m.app)
	if err != nil {
		return err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	r, err := core.GetRestore(ctx, vc, name)
	if err != nil {
		return err.Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Name:                %s\n", r.Name)
	fmt.Fprintf(&sb, "Backup:              %s\n", r.Spec.BackupName)
	fmt.Fprintf(&sb, "Phase:               %s\n", phaseOrDash(string(r.Status.Phase)))
	fmt.Fprintf(&sb, "Namespaces:          %s\n", joinOrDash(r.Spec.IncludedNamespaces))
	fmt.Fprintf(&sb, "Namespace Mappings:  %s\n", fmtNamespaceMapping(r.Spec.NamespaceMapping))
	fmt.Fprintf(&sb, "Warnings:            %d\n", r.Status.Warnings)
	fmt.Fprintf(&sb, "Errors:              %d\n", r.Status.Errors)
	fmt.Fprintf(&sb, "ValidationErrors:    %s", joinOrDash(r.Status.ValidationErrors))
	return sb.String()
}
