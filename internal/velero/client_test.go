package velero_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	velero "github.com/lucawalz/horizon/internal/velero"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTriggerBackup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- c.TriggerBackup(ctx, velerov1.BackupSpec{IncludedNamespaces: []string{"sentio-systems"}, StorageLocation: "default"}, "horizon-burst-sentio-systems-1", 10*time.Millisecond, 200*time.Millisecond)
	}()

	time.Sleep(30 * time.Millisecond)

	var b velerov1.Backup
	if err := fakeCl.Get(ctx, types.NamespacedName{Namespace: "velero", Name: "horizon-burst-sentio-systems-1"}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	b.Status.Phase = velerov1.BackupPhaseCompleted
	b.Status.Errors = 0
	if err := fakeCl.Update(ctx, &b); err != nil {
		t.Fatalf("update backup: %v", err)
	}

	if err := <-doneCh; err != nil {
		t.Fatalf("TriggerBackup: %v", err)
	}
	if b.Spec.StorageLocation != "default" {
		t.Errorf("StorageLocation = %q, want default", b.Spec.StorageLocation)
	}
	if len(b.Spec.IncludedNamespaces) != 1 || b.Spec.IncludedNamespaces[0] != "sentio-systems" {
		t.Errorf("IncludedNamespaces = %v, want [sentio-systems]", b.Spec.IncludedNamespaces)
	}
}

func TestTriggerBackup_Errors(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- c.TriggerBackup(ctx, velerov1.BackupSpec{IncludedNamespaces: []string{"sentio-systems"}, StorageLocation: "default"}, "horizon-burst-sentio-systems-1", 10*time.Millisecond, 200*time.Millisecond)
	}()

	time.Sleep(30 * time.Millisecond)

	var b velerov1.Backup
	if err := fakeCl.Get(ctx, types.NamespacedName{Namespace: "velero", Name: "horizon-burst-sentio-systems-1"}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	b.Status.Phase = velerov1.BackupPhaseCompleted
	b.Status.Errors = 3
	if err := fakeCl.Update(ctx, &b); err != nil {
		t.Fatalf("update backup: %v", err)
	}

	err := <-doneCh
	if err == nil {
		t.Fatal("expected error for backup with errors>0, got nil")
	}
	if !strings.Contains(err.Error(), "errors") {
		t.Errorf("error %q does not contain 'errors'", err.Error())
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error %q does not contain '3'", err.Error())
	}
}

func TestTriggerBackup_Timeout(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	err := c.TriggerBackup(ctx, velerov1.BackupSpec{IncludedNamespaces: []string{"ns"}, StorageLocation: "default"}, "name", 5*time.Millisecond, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q does not contain 'timeout'", err.Error())
	}
}

func newSchemeForTest(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return scheme
}

func TestTriggerRestore(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	spec := velerov1.RestoreSpec{
		BackupName:       "horizon-burst-sentio-systems-1",
		NamespaceMapping: map[string]string{"sentio-systems": "sentio-restore"},
	}
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- c.TriggerRestore(ctx, spec, "restore-1", 10*time.Millisecond, 200*time.Millisecond)
	}()

	time.Sleep(30 * time.Millisecond)

	var r velerov1.Restore
	if err := fakeCl.Get(ctx, types.NamespacedName{Namespace: "velero", Name: "restore-1"}, &r); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	r.Status.Phase = velerov1.RestorePhaseCompleted
	r.Status.Errors = 0
	if err := fakeCl.Update(ctx, &r); err != nil {
		t.Fatalf("update restore: %v", err)
	}

	if err := <-doneCh; err != nil {
		t.Fatalf("TriggerRestore: %v", err)
	}
	if r.Spec.BackupName != "horizon-burst-sentio-systems-1" {
		t.Errorf("BackupName = %q, want horizon-burst-sentio-systems-1", r.Spec.BackupName)
	}
	if r.Spec.NamespaceMapping["sentio-systems"] != "sentio-restore" {
		t.Errorf("NamespaceMapping = %v, want sentio-systems->sentio-restore", r.Spec.NamespaceMapping)
	}
}

func TestTriggerRestore_PartiallyFailed(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- c.TriggerRestore(ctx, velerov1.RestoreSpec{BackupName: "b"}, "restore-2", 10*time.Millisecond, 200*time.Millisecond)
	}()

	time.Sleep(30 * time.Millisecond)

	var r velerov1.Restore
	if err := fakeCl.Get(ctx, types.NamespacedName{Namespace: "velero", Name: "restore-2"}, &r); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	r.Status.Phase = velerov1.RestorePhasePartiallyFailed
	if err := fakeCl.Update(ctx, &r); err != nil {
		t.Fatalf("update restore: %v", err)
	}

	err := <-doneCh
	if err == nil {
		t.Fatal("expected error for PartiallyFailed restore, got nil")
	}
	if !strings.Contains(err.Error(), string(velerov1.RestorePhasePartiallyFailed)) {
		t.Errorf("error %q does not mention phase %s", err.Error(), velerov1.RestorePhasePartiallyFailed)
	}
}

func TestTriggerRestore_Timeout(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	err := c.TriggerRestore(ctx, velerov1.RestoreSpec{BackupName: "b"}, "restore-3", 5*time.Millisecond, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q does not contain 'timeout'", err.Error())
	}
}

func TestDeleteBackup(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	if err := c.DeleteBackup(ctx, "horizon-burst-sentio-systems-1"); err != nil {
		t.Fatalf("DeleteBackup: %v", err)
	}

	var list velerov1.DeleteBackupRequestList
	if err := fakeCl.List(ctx, &list); err != nil {
		t.Fatalf("list delete requests: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("DeleteBackupRequest count = %d, want 1", len(list.Items))
	}
	req := list.Items[0]
	if req.Namespace != "velero" {
		t.Errorf("namespace = %q, want velero", req.Namespace)
	}
	if req.Spec.BackupName != "horizon-burst-sentio-systems-1" {
		t.Errorf("BackupName = %q, want horizon-burst-sentio-systems-1", req.Spec.BackupName)
	}
	if !strings.HasPrefix(req.GenerateName, "horizon-burst-sentio-systems-1-") {
		t.Errorf("GenerateName = %q, want prefix horizon-burst-sentio-systems-1-", req.GenerateName)
	}
}

func TestListAndGetBackups(t *testing.T) {
	backup := &velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "velero"},
		Spec:       velerov1.BackupSpec{IncludedNamespaces: []string{"sentio-systems"}},
	}
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).WithObjects(backup).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	list, err := c.ListBackups(ctx)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(list) != 1 || list[0].Name != "b1" {
		t.Fatalf("ListBackups = %v, want [b1]", list)
	}

	got, err := c.GetBackup(ctx, "b1")
	if err != nil {
		t.Fatalf("GetBackup: %v", err)
	}
	if got.Name != "b1" {
		t.Errorf("GetBackup name = %q, want b1", got.Name)
	}
}

func TestCreateBackup_NoWait(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	spec := velerov1.BackupSpec{IncludedNamespaces: []string{"sentio-systems"}, StorageLocation: "default"}
	if err := c.CreateBackup(ctx, spec, "b-nowait"); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	var b velerov1.Backup
	if err := fakeCl.Get(ctx, types.NamespacedName{Namespace: "velero", Name: "b-nowait"}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if b.Status.Phase != "" {
		t.Errorf("CreateBackup must not set status phase, got %q", b.Status.Phase)
	}
	if len(b.Spec.IncludedNamespaces) != 1 || b.Spec.IncludedNamespaces[0] != "sentio-systems" {
		t.Errorf("IncludedNamespaces = %v, want [sentio-systems]", b.Spec.IncludedNamespaces)
	}
}

func TestScheduleLifecycle(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)
	ctx := context.Background()

	spec := velerov1.ScheduleSpec{
		Schedule: "0 3 * * *",
		Template: velerov1.BackupSpec{IncludedNamespaces: []string{"app"}, StorageLocation: "default"},
	}
	if err := c.CreateSchedule(ctx, spec, "nightly"); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	got, err := c.GetSchedule(ctx, "nightly")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.Spec.Schedule != "0 3 * * *" || len(got.Spec.Template.IncludedNamespaces) != 1 {
		t.Errorf("unexpected schedule %+v", got.Spec)
	}

	list, err := c.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(list) != 1 || list[0].Name != "nightly" {
		t.Fatalf("ListSchedules = %v, want [nightly]", list)
	}

	if err := c.DeleteSchedule(ctx, "nightly"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := c.GetSchedule(ctx, "nightly"); err == nil {
		t.Fatal("expected error getting deleted schedule")
	}
}

func TestBackupStorageLocationCreateAndList(t *testing.T) {
	fakeCl := fake.NewClientBuilder().WithScheme(newSchemeForTest(t)).Build()
	c := velero.NewClientWithCRClient(fakeCl)
	ctx := context.Background()

	spec := velerov1.BackupStorageLocationSpec{
		Provider:    "aws",
		StorageType: velerov1.StorageType{ObjectStorage: &velerov1.ObjectStorageLocation{Bucket: "horizon-backups"}},
	}
	if err := c.CreateBackupStorageLocation(ctx, spec, "secondary"); err != nil {
		t.Fatalf("CreateBackupStorageLocation: %v", err)
	}

	list, err := c.ListBackupStorageLocations(ctx)
	if err != nil {
		t.Fatalf("ListBackupStorageLocations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListBackupStorageLocations = %d, want 1", len(list))
	}
	bsl := list[0]
	if bsl.Name != "secondary" || bsl.Namespace != "velero" {
		t.Errorf("name/namespace = %q/%q, want secondary/velero", bsl.Name, bsl.Namespace)
	}
	if bsl.Spec.Provider != "aws" || bsl.Spec.ObjectStorage == nil || bsl.Spec.ObjectStorage.Bucket != "horizon-backups" {
		t.Errorf("unexpected BSL spec %+v", bsl.Spec)
	}
}

func TestNewClient_RateLimiterDisabled(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "kubeconfig")
	const kc = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: ""
`
	if err := os.WriteFile(kubeconfigPath, []byte(kc), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	c, err := velero.NewClient(kubeconfigPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	limiter := velero.ClientRateLimiterForTest(c)
	if limiter == nil {
		t.Fatal("rate limiter is nil; expected fake-always")
	}
	for i := 0; i < 100; i++ {
		if !limiter.TryAccept() {
			t.Fatalf("TryAccept returned false on iteration %d; default token-bucket limiter is in use", i)
		}
	}
}
