package velero_test

import (
	"context"
	"strings"
	"testing"
	"time"

	velero "github.com/lucawalz/horizon/internal/velero"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTriggerBackup(t *testing.T) {
	t.Skip("Plan 03 implements internal/velero/client.go")

	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- c.TriggerBackup(ctx, "sentio-systems", "horizon-burst-sentio-systems-1", 10*time.Millisecond, 200*time.Millisecond)
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
	t.Skip("Plan 03 implements internal/velero/client.go")

	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- c.TriggerBackup(ctx, "sentio-systems", "horizon-burst-sentio-systems-1", 10*time.Millisecond, 200*time.Millisecond)
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
	t.Skip("Plan 03 implements internal/velero/client.go")

	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := velero.NewClientWithCRClient(fakeCl)

	ctx := context.Background()
	err := c.TriggerBackup(ctx, "ns", "name", 5*time.Millisecond, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q does not contain 'timeout'", err.Error())
	}
}

