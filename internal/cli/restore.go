package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/lucawalz/horizon/internal/core"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func newRestoreCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Manage Velero restores",
	}
	cmd.AddCommand(newRestoreCreateCmd(app))
	cmd.AddCommand(newRestoreListCmd(app))
	cmd.AddCommand(newRestoreDescribeCmd(app))
	return cmd
}

func newRestoreCreateCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Velero restore",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestoreCreate(cmd, app)
		},
	}
	cmd.Flags().String("from-backup", "", "Backup to restore from (required)")
	cmd.Flags().StringSlice("include-namespaces", nil, "Namespaces to include")
	cmd.Flags().StringSlice("namespace-mappings", nil, "Namespace remappings (old:new)")
	cmd.Flags().String("name", "", "Restore name (auto-generated when omitted)")
	cmd.Flags().Bool("wait", false, "Wait for the restore to complete")
	_ = cmd.MarkFlagRequired("from-backup")
	return cmd
}

func runRestoreCreate(cmd *cobra.Command, app *App) error {
	spec, err := buildRestoreSpec(cmd)
	if err != nil {
		return err
	}

	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = core.DefaultRestoreName(spec.BackupName, nowFunc())
	}

	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}

	wait, _ := cmd.Flags().GetBool("wait")
	if err := core.CreateRestore(cmdContext(cmd), vc, spec, name, wait); err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}

func buildRestoreSpec(cmd *cobra.Command) (velerov1.RestoreSpec, error) {
	fromBackup, _ := cmd.Flags().GetString("from-backup")
	includeNs, _ := cmd.Flags().GetStringSlice("include-namespaces")
	mappings, _ := cmd.Flags().GetStringSlice("namespace-mappings")

	mapping := map[string]string{}
	for _, m := range mappings {
		parts := strings.Split(m, ":")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return velerov1.RestoreSpec{}, fmt.Errorf("restore: invalid namespace mapping %q, want old:new", m)
		}
		mapping[parts[0]] = parts[1]
	}

	spec := velerov1.RestoreSpec{
		BackupName:         fromBackup,
		IncludedNamespaces: includeNs,
	}
	if len(mapping) > 0 {
		spec.NamespaceMapping = mapping
	}
	return spec, nil
}

func newRestoreListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Velero restores",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestoreList(cmd, app)
		},
	}
}

func runRestoreList(cmd *cobra.Command, app *App) error {
	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}
	restores, err := core.ListRestores(cmdContext(cmd), vc)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "NAME\tBACKUP\tSTATUS\tWARNINGS\tERRORS")
	for i := range restores {
		r := &restores[i]
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n",
			r.Name, r.Spec.BackupName, phaseOrDash(string(r.Status.Phase)),
			r.Status.Warnings, r.Status.Errors)
	}
	return nil
}

func newRestoreDescribeCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "describe <name>",
		Short: "Describe a Velero restore",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestoreDescribe(cmd, app, args[0])
		},
	}
}

func runRestoreDescribe(cmd *cobra.Command, app *App, name string) error {
	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}
	r, err := core.GetRestore(cmdContext(cmd), vc, name)
	if err != nil {
		return err
	}
	fmt.Printf("Name:                %s\n", r.Name)
	fmt.Printf("Backup:              %s\n", r.Spec.BackupName)
	fmt.Printf("Phase:               %s\n", phaseOrDash(string(r.Status.Phase)))
	fmt.Printf("Namespaces:          %s\n", joinOrDash(r.Spec.IncludedNamespaces))
	fmt.Printf("Namespace Mappings:  %s\n", fmtNamespaceMapping(r.Spec.NamespaceMapping))
	fmt.Printf("Warnings:            %d\n", r.Status.Warnings)
	fmt.Printf("Errors:              %d\n", r.Status.Errors)
	fmt.Printf("ValidationErrors:    %s\n", joinOrDash(r.Status.ValidationErrors))
	return nil
}

func NewRestoreCmdForTest(app *App) *cobra.Command { return newRestoreCmd(app) }

func BuildRestoreSpecForTest(cmd *cobra.Command) (velerov1.RestoreSpec, error) {
	return buildRestoreSpec(cmd)
}

func fmtNamespaceMapping(m map[string]string) string {
	if len(m) == 0 {
		return "-"
	}
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+":"+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}
