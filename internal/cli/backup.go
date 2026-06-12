package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lucawalz/horizon/internal/velero"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultStorageLocation = "default"
	defaultBackupTTL       = 168 * time.Hour
	backupTimeLayout       = "2006-01-02 15:04"
	backupNameTimeLayout   = "20060102-150405"
)

type veleroClient interface {
	CreateBackup(ctx context.Context, spec velerov1.BackupSpec, name string) error
	TriggerBackup(ctx context.Context, spec velerov1.BackupSpec, name string, poll, timeout time.Duration) error
	CreateRestore(ctx context.Context, spec velerov1.RestoreSpec, name string) error
	TriggerRestore(ctx context.Context, spec velerov1.RestoreSpec, name string, poll, timeout time.Duration) error
	ListBackups(ctx context.Context) ([]velerov1.Backup, error)
	GetBackup(ctx context.Context, name string) (*velerov1.Backup, error)
	DeleteBackup(ctx context.Context, name string) error
	ListRestores(ctx context.Context) ([]velerov1.Restore, error)
	GetRestore(ctx context.Context, name string) (*velerov1.Restore, error)
}

var (
	testVeleroClient veleroClient
	nowFunc          = time.Now
)

func SetVeleroClientForTest(vc veleroClient) (restore func()) {
	prev := testVeleroClient
	testVeleroClient = vc
	return func() { testVeleroClient = prev }
}

func SetNowFuncForTest(fn func() time.Time) (restore func()) {
	prev := nowFunc
	nowFunc = fn
	return func() { nowFunc = prev }
}

func resolveVeleroClient(app *App) (veleroClient, error) {
	if testVeleroClient != nil {
		return testVeleroClient, nil
	}
	return newVeleroClient(app)
}

func newVeleroClient(app *App) (veleroClient, error) {
	vc, err := velero.NewClient(app.Config.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("velero client: %w", err)
	}
	return vc, nil
}

func newBackupCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage Velero backups",
	}
	cmd.AddCommand(newBackupCreateCmd(app))
	cmd.AddCommand(newBackupListCmd(app))
	cmd.AddCommand(newBackupDescribeCmd(app))
	cmd.AddCommand(newBackupDeleteCmd(app))
	return cmd
}

func newBackupCreateCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Velero backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupCreate(cmd, app)
		},
	}
	cmd.Flags().StringSlice("include-namespaces", nil, "Namespaces to include")
	cmd.Flags().StringSlice("exclude-namespaces", nil, "Namespaces to exclude")
	cmd.Flags().StringSlice("include-resources", nil, "Resources to include")
	cmd.Flags().String("selector", "", "Label selector (k=v,k2=v2)")
	cmd.Flags().String("storage-location", defaultStorageLocation, "Backup storage location")
	cmd.Flags().Duration("ttl", defaultBackupTTL, "Backup retention duration")
	cmd.Flags().Bool("snapshot-volumes", true, "Snapshot persistent volumes")
	cmd.Flags().String("name", "", "Backup name (auto-generated when omitted)")
	cmd.Flags().Bool("wait", false, "Wait for the backup to complete")
	return cmd
}

func runBackupCreate(cmd *cobra.Command, app *App) error {
	spec, err := buildBackupSpec(cmd)
	if err != nil {
		return err
	}
	if backupScopeEmpty(spec) {
		fmt.Fprintln(os.Stderr, "backing up all namespaces")
	}

	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = defaultBackupName(spec.IncludedNamespaces)
	}

	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}

	ctx := cmdContext(cmd)
	wait, _ := cmd.Flags().GetBool("wait")
	if wait {
		if err := vc.TriggerBackup(ctx, spec, name, 5*time.Second, 10*time.Minute); err != nil {
			return err
		}
	} else if err := vc.CreateBackup(ctx, spec, name); err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}

func buildBackupSpec(cmd *cobra.Command) (velerov1.BackupSpec, error) {
	includeNs, _ := cmd.Flags().GetStringSlice("include-namespaces")
	excludeNs, _ := cmd.Flags().GetStringSlice("exclude-namespaces")
	includeRes, _ := cmd.Flags().GetStringSlice("include-resources")
	storage, _ := cmd.Flags().GetString("storage-location")
	ttl, _ := cmd.Flags().GetDuration("ttl")
	snapshot, _ := cmd.Flags().GetBool("snapshot-volumes")
	selector, _ := cmd.Flags().GetString("selector")

	spec := velerov1.BackupSpec{
		IncludedNamespaces: includeNs,
		ExcludedNamespaces: excludeNs,
		IncludedResources:  includeRes,
		StorageLocation:    storage,
		TTL:                metav1.Duration{Duration: ttl},
		SnapshotVolumes:    &snapshot,
	}

	if selector != "" {
		ls, err := metav1.ParseToLabelSelector(selector)
		if err != nil {
			return velerov1.BackupSpec{}, fmt.Errorf("backup: invalid selector %q: %w", selector, err)
		}
		spec.LabelSelector = ls
	}
	return spec, nil
}

func backupScopeEmpty(spec velerov1.BackupSpec) bool {
	return len(spec.IncludedNamespaces) == 0 &&
		len(spec.ExcludedNamespaces) == 0 &&
		len(spec.IncludedResources) == 0 &&
		spec.LabelSelector == nil
}

func defaultBackupName(includeNs []string) string {
	scope := "all"
	if len(includeNs) > 0 {
		scope = includeNs[0]
	}
	return fmt.Sprintf("horizon-%s-%s", scope, nowFunc().UTC().Format(backupNameTimeLayout))
}

func newBackupListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Velero backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupList(cmd, app)
		},
	}
}

func runBackupList(cmd *cobra.Command, app *App) error {
	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}
	backups, err := vc.ListBackups(cmdContext(cmd))
	if err != nil {
		return err
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreationTimestamp.After(backups[j].CreationTimestamp.Time)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "NAME\tSTATUS\tCREATED\tEXPIRES\tERRORS")
	for i := range backups {
		b := &backups[i]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
			b.Name, phaseOrDash(string(b.Status.Phase)),
			fmtTime(&b.CreationTimestamp), fmtTime(b.Status.Expiration), b.Status.Errors)
	}
	return nil
}

func newBackupDescribeCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "describe <name>",
		Short: "Describe a Velero backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupDescribe(cmd, app, args[0])
		},
	}
}

func runBackupDescribe(cmd *cobra.Command, app *App, name string) error {
	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}
	b, err := vc.GetBackup(cmdContext(cmd), name)
	if err != nil {
		return err
	}
	fmt.Printf("Name:              %s\n", b.Name)
	fmt.Printf("Phase:             %s\n", phaseOrDash(string(b.Status.Phase)))
	fmt.Printf("Namespaces:        %s\n", joinOrDash(b.Spec.IncludedNamespaces))
	fmt.Printf("Storage Location:  %s\n", b.Spec.StorageLocation)
	fmt.Printf("Created:           %s\n", fmtTime(&b.CreationTimestamp))
	fmt.Printf("Expires:           %s\n", fmtTime(b.Status.Expiration))
	fmt.Printf("Errors:            %d\n", b.Status.Errors)
	fmt.Printf("Warnings:          %d\n", b.Status.Warnings)
	fmt.Printf("ValidationErrors:  %s\n", joinOrDash(b.Status.ValidationErrors))
	return nil
}

func newBackupDeleteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a Velero backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupDelete(cmd, app, args[0])
		},
	}
}

func runBackupDelete(cmd *cobra.Command, app *App, name string) error {
	vc, err := resolveVeleroClient(app)
	if err != nil {
		return err
	}
	if err := vc.DeleteBackup(cmdContext(cmd), name); err != nil {
		return err
	}
	fmt.Println("delete request submitted; velero removes the backup and its snapshots in the background")
	return nil
}

func NewBackupCmdForTest(app *App) *cobra.Command { return newBackupCmd(app) }

func BuildBackupSpecForTest(cmd *cobra.Command) (velerov1.BackupSpec, error) {
	return buildBackupSpec(cmd)
}

func DefaultBackupNameForTest(includeNs []string) string { return defaultBackupName(includeNs) }

func cmdContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func fmtTime(t *metav1.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Format(backupTimeLayout)
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
